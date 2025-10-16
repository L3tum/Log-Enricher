package bufferpool

import (
	"bytes"
	"sync"
)

var (
	// byteBufferPool is a pool of bytes.Buffer objects to reduce memory allocations.
	byteBufferPool = sync.Pool{
		New: func() interface{} {
			// The initial size is chosen to be large enough for typical log lines
			// to avoid reallocations, but not so large as to waste memory.
			return bytes.NewBuffer(make([]byte, 0, 512))
		},
	}
)

// GetByteBuffer retrieves a buffer from the pool.
func GetByteBuffer() *bytes.Buffer {
	return byteBufferPool.Get().(*bytes.Buffer)
}

// PutByteBuffer returns a buffer to the pool for reuse.
// The buffer's contents are reset before it's put back.
func PutByteBuffer(buf *bytes.Buffer) {
	buf.Reset()
	byteBufferPool.Put(buf)
}
