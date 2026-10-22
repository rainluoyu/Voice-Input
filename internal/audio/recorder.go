package audio

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Recorder interface {
	Start(context.Context, string, int, func(float64)) error
	Stop(context.Context) (Data, error)
}
type Native struct {
	mu           sync.Mutex
	stop         chan struct{}
	done         chan struct{}
	once         sync.Once
	data         Data
	err          error
	mutePlayback bool
}

func New() *Native { return NewWithMute(false) }

func NewWithMute(mutePlayback bool) *Native {
	return &Native{stop: make(chan struct{}), done: make(chan struct{}), mutePlayback: mutePlayback}
}

func (n *Native) start(ctx context.Context, timeout time.Duration, capture func(context.Context, chan<- error) (Data, error)) error {
	captureCtx, cancel := context.WithCancel(ctx)
	ready := make(chan error, 1)
	go func() {
		defer cancel()
		data, err := capture(captureCtx, ready)
		n.finish(data, err)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var err error
	select {
	case <-timer.C:
		err = ErrUnavailable
	case err = <-ready:
		if err == nil {
			if err = ctx.Err(); err == nil {
				return nil
			}
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	// A failed Start must not leave a late capture running or hide restore errors.
	cancel()
	<-n.done
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil && !errors.Is(n.err, context.Canceled) && !errors.Is(n.err, context.DeadlineExceeded) {
		return n.err
	}
	return err
}
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
