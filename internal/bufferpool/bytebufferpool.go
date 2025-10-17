package bufferpool

import (
	"bytes"
	"sync"
)

const INITIAL_BUFFER_SIZE = 2048
const MAX_BUFFER_SIZE = 4096

// MAX_IN_FLIGHT_BYTE_BUFFERS defines the maximum number of byte buffers that can be
// "in-flight" (i.e., checked out from the pool) at any given time.
// This prevents unbounded memory growth under high load.
// If we assume 4KB buffers, this is 20MB of memory.
const MAX_IN_FLIGHT_BYTE_BUFFERS = 5000

var (
	// byteBufferPool is a pool of bytes.Buffer objects to reduce memory allocations.
	byteBufferPool = sync.Pool{
		New: func() interface{} {
			// The initial size is chosen to be large enough for typical log lines
			// to avoid reallocations, but not so large as to waste memory.
			return bytes.NewBuffer(make([]byte, 0, INITIAL_BUFFER_SIZE))
		},
	}

	// byteBufferSemaphore is a channel used as a semaphore to limit the number
	// of in-flight byte buffers.
	byteBufferSemaphore = make(chan struct{}, MAX_IN_FLIGHT_BYTE_BUFFERS)
)

// GetByteBuffer retrieves a buffer from the pool. It will block if the maximum
// number of in-flight buffers has been reached.
func GetByteBuffer() *bytes.Buffer {
	// Acquire a token from the semaphore. This will block if the channel is full.
	byteBufferSemaphore <- struct{}{}
	return byteBufferPool.Get().(*bytes.Buffer)
}

// PutByteBuffer returns a buffer to the pool for reuse.
// The buffer's contents are reset before it's put back.
func PutByteBuffer(buf *bytes.Buffer) {
	<-byteBufferSemaphore

	// make sure we don't put too large buffers into the pool
	if buf.Len() > MAX_BUFFER_SIZE {
		return
	}

	buf.Reset()
	byteBufferPool.Put(buf)
}
