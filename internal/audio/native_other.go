//go:build !windows

package audio

import "context"

func Devices() ([]Device, error) { return nil, ErrUnavailable }
func (n *Native) Start(context.Context, string, int, func(float64)) error {
	n.finish(Data{}, ErrUnavailable)
	return ErrUnavailable
}
