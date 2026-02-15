package bufferpool

// ObjectPool manages a pool of PooledObject instances.
type ObjectPool[T any] struct {
	pool  chan T // Channel for available objects, directly holding values
	size  int
	new   func() T // Function to create new objects, returning T
	clear func(T)  // Function to clear objects, accepting T
}

// NewObjectPool creates a new pool with the specified size.
func NewObjectPool[T any](size int, new func() T, clear func(T)) *ObjectPool[T] {
	// The channel now holds PooledObject directly.
	pool := make(chan T, size)
	op := &ObjectPool[T]{
		pool:  pool,
		size:  size,
		new:   new,
		clear: clear,
	}

	// Pre-fill the pool with initial objects
	for i := 0; i < size; i++ {
		// Call the user-provided new function directly to create the object
		// and send it to the channel.
		op.pool <- op.new()
	}
	return op
}

// Acquire gets an object from the pool. Blocks if no objects are available.
// It returns the PooledObject directly.
func (op *ObjectPool[T]) Acquire() T {
	obj := <-op.pool
	return obj
}

// Release returns an object to the pool.
// It accepts the PooledObject directly.
func (op *ObjectPool[T]) Release(obj T) {
	op.clear(obj)
	op.pool <- obj
}
