//go:build !windows

package settings

import "errors"

type NativeVault struct{}

func (NativeVault) Get(string) (string, error) { return "", ErrNoKey }
func (NativeVault) Set(string, string) error {
	return errors.New("凭据存储仅支持 Windows Credential Manager")
}
func (NativeVault) Delete(string) error {
	return errors.New("凭据存储仅支持 Windows Credential Manager")
}
