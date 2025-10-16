package bufferpool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestByteBufferPool(t *testing.T) {
	t.Run("Get and Put", func(t *testing.T) {
		// Get a buffer and write to it
		buf := GetByteBuffer()
		buf.WriteString("hello")
		assert.Equal(t, "hello", buf.String())

		// Put it back
		PutByteBuffer(buf)

		// Get another buffer (might be the same one)
		buf2 := GetByteBuffer()
		// It should be empty because PutByteBuffer resets it
		assert.Equal(t, 0, buf2.Len())
	})

	t.Run("Concurrency test", func(t *testing.T) {
		// This test is most effective when run with the -race flag
		// go test -race ./...
		var wg sync.WaitGroup
		numGoroutines := 100
		numOperations := 1000

		wg.Add(numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					buf := GetByteBuffer()
					buf.WriteString("test")
					assert.Equal(t, "test", buf.String())
					PutByteBuffer(buf)
				}
			}()
		}
		wg.Wait()
	})
}
