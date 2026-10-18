//go:build !windows

package wininput

import (
	"context"
	"errors"
)

type Native struct{}

func (Native) Prepare(context.Context, string) (func() error, error) {
	return nil, errors.New("自动输入仅支持 Windows")
}
