package tailer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		line, err := t.reader.ReadBytes('\n')

		// If we read a line, process it. This handles the case where a file
		// ends without a newline character before hitting EOF.
		if len(line) > 0 {
			buf := bufferpool.GetByteBuffer()
			buf.Write(line)
			t.Lines <- &Line{Buffer: buf}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				// Reached the end of the file, check for rotation/truncation.
				if e := t.handleEOF(); e != nil {
					t.Errors <- e
					return
				}
				// Continue loop to wait for more data.
				continue
			}
			// A non-EOF error occurred.
			t.Errors <- err
			return
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
		return err
	}

	// Check if the file has been truncated.
	if info.Size() < pos {
		log.Printf("File %s was truncated, seeking to beginning.", t.path)
		if _, err := t.file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		t.reader.Reset(t.file)
		return nil
	}

	// Check if the file has been rotated or deleted.
	infoOnDisk, err := os.Stat(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("File %s disappeared, attempting to reopen.", t.path)
			return t.reopen()
		}
		return err
	}

	// Compare the file on disk with the file we have open.
	if !os.SameFile(info, infoOnDisk) {
		log.Printf("File %s was rotated, opening new file.", t.path)
		return t.reopen()
	}

	// The file is the same and not truncated, so just wait for more content.
	time.Sleep(250 * time.Millisecond)
	return nil
}

// reopen attempts to close the current file and open the new one at the same path.
// This is used for log rotation and deletion/recreation.
func (t *Tailer) reopen() error {
	t.file.Close()

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
				return nil
			}

			if os.IsNotExist(err) {
				// File not there yet, wait a bit.
				time.Sleep(500 * time.Millisecond)
				continue
			}

			// Another type of error occurred.
			return err
		}
	}
}
