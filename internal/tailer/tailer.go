package tailer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// Line represents a single line read from the tailed file.
type Line struct {
	Buffer []byte
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
func NewTailer(parentCtx context.Context, path string, offset int64, whence int) *Tailer {
	ctx, cancel := context.WithCancel(parentCtx)
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

	// Resume is line-based when reading from the start of the file.
	// In this mode, offset is the number of lines to skip.
	if t.whence == io.SeekStart && t.offset > 0 {
		if err := t.seekToLineOffset(); err != nil {
			t.file.Close()
			return err
		}
		return nil
	}

	// Seek to the desired position.
	if _, err := t.file.Seek(t.offset, t.whence); err != nil {
		t.file.Close() // Close file on seek error
		return err
	}

	return nil
}

func (t *Tailer) seekToLineOffset() error {
	reader := bufio.NewReader(t.file)
	var linesSkipped int64

	for linesSkipped < t.offset {
		_, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		linesSkipped++
	}

	pos, err := t.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	// bufio.Reader may have read ahead; rewind to the first unread byte.
	pos -= int64(reader.Buffered())
	_, err = t.file.Seek(pos, io.SeekStart)
	return err
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
	buf := new(bytes.Buffer)

	for {
		// Check for context cancellation before attempting to read or process.
		select {
		case <-t.ctx.Done():
			return
		default:
			// Continue
		}

		var err error
		var lineBytes []byte
		var isPrefix bool = true // Assume prefix initially, ReadLine will update

		// Read a complete line, potentially in multiple chunks.
		// This loop now ensures that any data read is written to the buffer,
		// even if an EOF occurs on the last chunk of a long line.
		for {
			lineBytes, isPrefix, err = t.reader.ReadLine()
			if len(lineBytes) > 0 { // Always write any data read
				buf.Write(lineBytes)
			}

			if err != nil {
				if errors.Is(err, io.EOF) {
					// If EOF, and we have some data in buf, that's the last (possibly partial) line.
					if buf.Len() > 0 {
						break // Treat the accumulated buffer as a complete line
					} else {
						// Pure EOF, no data.
						if e := t.handleEOF(); e != nil {
							t.Errors <- e
							return
						}
						continue // Continue loop to wait for more data
					}
				} else {
					// A non-EOF error occurred.
					t.Errors <- err
					return
				}
			}

			if !isPrefix { // If it's not a prefix and no error, we have a complete line
				break
			}
		}

		bytesBuf := bytes.Clone(buf.Bytes())

		// Reset backoff since we successfully read a line
		t.resetBackoff()

		// If we reached here, buf contains a line (either full or partial before EOF).
		// Send the line to the Lines channel.
		select {
		case <-t.ctx.Done():
			// Context canceled while trying to send the line.
			return
		case t.Lines <- &Line{Buffer: bytesBuf}:
			buf.Reset()
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
			slog.Warn("File disappeared during stat, attempting to reopen.", "path", t.path)
			return t.reopen()
		}
		return err
	}

	// Case 1: File was truncated.
	if info.Size() < pos {
		slog.Info("File was truncated (size < current pos), seeking to beginning.", "path", t.path, "size", info.Size(), "pos", pos)
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
		//log.Printf("File %s has new data (size %d > current pos %d), attempting to read immediately.", t.path, info.Size(), pos)
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
			slog.Warn("File disappeared, attempting to reopen.", "path", t.path)
			// On file change reopen() already calls resetBackoff() internally
			return t.reopen()
		}
		return err
	}

	// Compare the file on disk with the file we have open.
	if !os.SameFile(info, infoOnDisk) {
		slog.Info("File was rotated, opening new file.", "path", t.path)
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
				slog.Debug("Successfully reopened file", "path", t.path)
				t.resetBackoff() // Reset backoff on successful reopen
				return nil
			}

			if os.IsNotExist(err) {
				reopenAttempts++
				if reopenAttempts > MAX_REOPEN_ATTEMPTS {
					slog.Error("Failed to reopen file. Giving up.", "path", t.path, "attempts", MAX_REOPEN_ATTEMPTS)
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
	t.currentBackoff = min(t.currentBackoff*BACKOFF_FACTOR, MAX_BACKOFF_DELAY)
}
