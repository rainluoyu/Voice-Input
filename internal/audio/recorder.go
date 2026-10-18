package audio

import (
	"context"
	"sync"
)

type Recorder interface {
	Start(context.Context, string, int, func(float64)) error
	Stop(context.Context) (Data, error)
}
type Native struct {
	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
	once sync.Once
	data Data
	err  error
}

func New() *Native { return &Native{stop: make(chan struct{}), done: make(chan struct{})} }
func (n *Native) Stop(ctx context.Context) (Data, error) {
	n.once.Do(func() { close(n.stop) })
	select {
	case <-ctx.Done():
		return Data{}, ctx.Err()
	case <-n.done:
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.data, n.err
	}
}
func (n *Native) finish(data Data, err error) {
	n.mu.Lock()
	n.data, n.err = data, err
	n.mu.Unlock()
	close(n.done)
}
