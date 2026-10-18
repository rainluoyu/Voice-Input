//go:build windows

package settings

import (
	"errors"

	"github.com/danieljoos/wincred"

	"voice_input/internal/i18n"
)

type NativeVault struct{}

func (NativeVault) Get(kind string) (string, error) {
	name, err := KeyName(kind)
	if err != nil {
		return "", err
	}
	cred, err := wincred.GetGenericCredential(name)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return "", ErrNoKey
	}
	if err != nil {
		return "", i18n.Error("settings.vault_read")
	}
	return string(cred.CredentialBlob), nil
}
func (NativeVault) Set(kind, key string) error {
	name, err := KeyName(kind)
	if err != nil {
		return err
	}
	if err = ValidKey(key); err != nil {
		return err
	}
	cred := wincred.NewGenericCredential(name)
	cred.CredentialBlob = []byte(key)
	cred.Persist = wincred.PersistLocalMachine
	if cred.Write() != nil {
		return i18n.Error("settings.vault_save")
	}
	return nil
}
func (NativeVault) Delete(kind string) error {
	name, err := KeyName(kind)
	if err != nil {
		return err
	}
	cred, err := wincred.GetGenericCredential(name)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return nil
	}
	if err != nil {
		return i18n.Error("settings.vault_read_delete")
	}
	if cred.Delete() != nil {
		return i18n.Error("settings.vault_delete")
	}
	return nil
}
