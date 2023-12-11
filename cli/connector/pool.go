package connector

import (
	"errors"
)

var (
	errFailedToConnect = errors.New("failed to connect to any instance")
)

// Pool is a very simple connection pool. It uses a one active connection
// and switches to another on an error.
type Pool struct {
	opts         []ConnectOpts
	current      Connector
	currentIndex int
}

// ConnectPool creates a connection pool object. It makes sure that it can
// connect to at least one instance.
func ConnectPool(opts []ConnectOpts) (*Pool, error) {
	// Protects from external modifications of the data in the slice.
	cpy := make([]ConnectOpts, len(opts))
	copy(cpy, opts)

	for i, opt := range cpy {
		conn, err := Connect(opt)
		if err == nil {
			return &Pool{
				opts:         cpy,
				current:      conn,
				currentIndex: i,
			}, nil
		}
	}
	return nil, errFailedToConnect
}

// Eval executes the expression on each connectable instance until
// success.
func (pool *Pool) Eval(expr string, args []any, opts RequestOpts) ([]any, error) {
	var err error
	for i := 0; i < len(pool.opts); i++ {
		if pool.current == nil {
			conn, err := Connect(pool.opts[pool.currentIndex])
			if err != nil {
				pool.currentIndex = (pool.currentIndex + 1) % len(pool.opts)
				continue
			}
			pool.current = conn
		}

		var ret []any
		ret, err = pool.current.Eval(expr, args, opts)
		if err == nil {
			return ret, nil
		}

		pool.current.Close()
		pool.current = nil
		pool.currentIndex = (pool.currentIndex + 1) % len(pool.opts)
	}

	if err == nil {
		err = errFailedToConnect
	} // Else it contains a last error from pool.current.Eval().
	return nil, err
}

// Close closes the pool.
func (pool *Pool) Close() error {
	if pool.current != nil {
		return pool.current.Close()
	}
	return nil
}
