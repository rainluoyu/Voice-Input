//go:build !windows

package settings

import "voice_input/internal/i18n"

type NativeVault struct{}

func (NativeVault) Get(string) (string, error) { return "", ErrNoKey }
func (NativeVault) Set(string, string) error {
	return i18n.Error("settings.vault_platform")
}
func (NativeVault) Delete(string) error {
	return i18n.Error("settings.vault_platform")
}
