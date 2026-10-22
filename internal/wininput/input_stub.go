//go:build !windows

package wininput

import (
	"context"

	"voice_input/internal/i18n"
)

type Native struct{}

func (Native) Copy(string) error { return i18n.Error("input.platform") }

func (Native) Prepare(context.Context, string) (func() error, error) {
	return nil, i18n.Error("input.platform")
}
