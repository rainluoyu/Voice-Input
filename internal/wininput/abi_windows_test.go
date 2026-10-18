//go:build windows && amd64

package wininput

import (
	"testing"
	"unsafe"
)

func TestWindowsABISizes(t *testing.T) {
	if unsafe.Sizeof(keyboardInput{}) != 40 {
		t.Fatal("INPUT size must be 40")
	}
}
