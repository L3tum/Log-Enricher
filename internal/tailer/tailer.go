package tailer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log-enricher/internal/bufferpool"
	"os"
	"time"
)

// Line represents a single line read from the tailed file.
// It uses a *bytes.Buffer from a pool to avoid allocations.
type Line struct {
	Buffer *bytes.Buffer
}

const (
	INITIAL_BACKOFF_DELAY = 250 * time.Millisecond
	MAX_BACKOFF_DELAY     = 5 * time.Second
	BACKOFF_FACTOR        = 2
	MAX_REOPEN_ATTEMPTS   = 10 // Maximum attempts to reopen a missing file
)

// Tailer is a struct that tails a file.
type Tailer struct {
	path   string
	Lines  chan *Line
	Errors chan error

	file   *os.File
	reader *bufio.Reader
	ctx    context.Context
	cancel context.CancelFunc

	offset int64
	whence int

	// Backoff fields for EOF handling
	currentBackoff time.Duration
	eofCount       int
}

// NewTailer creates a new Tailer.
func NewTailer(path string, offset int64, whence int) *Tailer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Tailer{
		path:   path,
		Lines:  make(chan *Line),
		Errors: make(chan error),
		ctx:    ctx,
		cancel: cancel,
		offset: offset,
		whence: whence,

		currentBackoff: INITIAL_BACKOFF_DELAY,
		eofCount:       0,
	}
}

// Start begins the tailing process.
func (t *Tailer) Start() {
	go t.run()
}

// Stop stops the tailer.
func (t *Tailer) Stop() {
	t.cancel()
}

func (t *Tailer) openAndSeek() error {
	var err error
	t.file, err = os.Open(t.path)
	if err != nil {
		return err
	}

	// Seek to the desired position.
	if _, err := t.file.Seek(t.offset, t.whence); err != nil {
		t.file.Close() // Close file on seek error
		return err
	}

	return nil
}

func (t *Tailer) run() {
	defer close(t.Lines)
	defer close(t.Errors)

	if err := t.openAndSeek(); err != nil {
		t.Errors <- err
		return
	}
	defer t.file.Close()

	t.reader = bufio.NewReader(t.file)

	for {
		// Check for context cancellation before attempting to read or process.
		select {
		case <-t.ctx.Done():
			return
		default:
			// Continue
		}

		buf := bufferpool.GetByteBuffer() // Acquire buffer from pool
		var err error
		var lineBytes []byte
		var isPrefix bool = true // Assume prefix initially, ReadLine will update

		// Read a complete line, potentially in multiple chunks
		for isPrefix && err == nil {
			lineBytes, isPrefix, err = t.reader.ReadLine()
			if err == nil {
				buf.Write(lineBytes)
			}
		}

		// Handle errors from ReadLine
		if err != nil {
			if errors.Is(err, io.EOF) {
				// If EOF and no data was read into buf, it's a pure EOF.
				if buf.Len() == 0 {
					bufferpool.PutByteBuffer(buf) // Return unused buffer
					if e := t.handleEOF(); e != nil {
						t.Errors <- e
						return
					}
					continue // Continue loop to wait for more data
				}
				// If EOF but data was read, treat it as a valid line.
				// The next iteration will hit pure EOF.
			} else {
				// A non-EOF error occurred.
				bufferpool.PutByteBuffer(buf) // Return buffer on error
				t.Errors <- err
				return
			}
		}

		// Reset backoff since we successfully read a line
		t.resetBackoff()

		// If we reached here, buf contains a line (either full or partial before EOF).
		// Send the line to the Lines channel.
		select {
		case <-t.ctx.Done():
			// Context canceled while trying to send the line.
			// Return the buffer to the pool before exiting.
			bufferpool.PutByteBuffer(buf)
			return
		case t.Lines <- &Line{Buffer: buf}:
			// Successfully sent the line. The watcher.LogWatcher#processLine function
			// is now responsible for returning this buffer to the pool.
		}
	}
}

// handleEOF is called when we've hit the end of the file. It's responsible for
// handling file truncation and rotation.
func (t *Tailer) handleEOF() error {
	pos, err := t.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	info, err := t.file.Stat()
	if err != nil {
		// If we can't stat the file, it might have been deleted or permissions changed.
		// Try to reopen.
		if os.IsNotExist(err) {
			log.Printf("File %s disappeared during stat, attempting to reopen.", t.path)
			return t.reopen()
		}
		return err
	}

	// Case 1: File was truncated.
	if info.Size() < pos {
		log.Printf("File %s was truncated (size %d < current pos %d), seeking to beginning.", t.path, info.Size(), pos)
		t.resetBackoff() // Reset backoff on file change
		if _, err := t.file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		t.reader.Reset(t.file) // Reset bufio.Reader to clear its buffer and read from start
		return nil
	}

	// Case 2: New data has been appended (file size increased).
	// In this scenario, we should not backoff, but immediately try to read again.
	if info.Size() > pos {
		log.Printf("File %s has new data (size %d > current pos %d), attempting to read immediately.", t.path, info.Size(), pos)
		t.resetBackoff() // Reset backoff as new data is available
		// No need to seek or reset reader here, the next ReadLine will pick up new data
		// because the underlying file has grown. The bufio.Reader will eventually
		// read from the underlying file.
		return nil
	}

	// Case 3: File was rotated or deleted (check inode/existence).
	// This check comes after truncation and new data checks, as those are more immediate.
	infoOnDisk, err := os.Stat(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("File %s disappeared, attempting to reopen.", t.path)
			// On file change reopen() already calls resetBackoff() internally
			return t.reopen()
		}
		return err
	}

	// Compare the file on disk with the file we have open.
	if !os.SameFile(info, infoOnDisk) {
		log.Printf("File %s was rotated, opening new file.", t.path)
		// On file change reopen() already calls resetBackoff() internally
		return t.reopen()
	}

	t.applyBackoff()
	return nil
}

// reopen attempts to close the current file and open the new one at the same path.
// This is used for log rotation and deletion/recreation.
func (t *Tailer) reopen() error {
	t.file.Close()

	reopenAttempts := 0 // Local counter for this specific reopen cycle

	for {
		select {
		case <-t.ctx.Done():
			return t.ctx.Err()
		default:
			file, err := os.Open(t.path)
			if err == nil {
				// Success!
				t.file = file
				t.reader.Reset(t.file)
				log.Printf("Successfully reopened file %s", t.path)
				t.resetBackoff() // Reset backoff on successful reopen
				return nil
			}

			if os.IsNotExist(err) {
				reopenAttempts++
				if reopenAttempts > MAX_REOPEN_ATTEMPTS {
					log.Printf("Failed to reopen file %s after %d attempts. Giving up.", t.path, MAX_REOPEN_ATTEMPTS)
					return fmt.Errorf("failed to reopen file %s after %d attempts", t.path, MAX_REOPEN_ATTEMPTS)
				}
				// File not there yet, wait a bit.
				time.Sleep(500 * time.Millisecond)
				continue
			}

			// Another type of error occurred.
			return err
		}
	}
}

// resetBackoff resets the backoff state to its initial values.
func (t *Tailer) resetBackoff() {
	t.currentBackoff = INITIAL_BACKOFF_DELAY
	t.eofCount = 0
}

// applyBackoff sleeps for the current backoff duration and then increases it exponentially.
func (t *Tailer) applyBackoff() {
	time.Sleep(t.currentBackoff)

	t.eofCount++
	nextBackoff := t.currentBackoff * BACKOFF_FACTOR
	if nextBackoff > MAX_BACKOFF_DELAY {
		t.currentBackoff = MAX_BACKOFF_DELAY
	} else {
		t.currentBackoff = nextBackoff
	}
}
