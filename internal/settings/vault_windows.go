//go:build windows

package settings

import (
	"errors"
	"github.com/danieljoos/wincred"
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
		return "", errors.New("Windows Credential Manager 读取失败")
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
		return errors.New("Windows Credential Manager 保存失败")
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
		return errors.New("Windows Credential Manager 读取失败；未删除密钥")
	}
	if cred.Delete() != nil {
		return errors.New("Windows Credential Manager 删除失败")
	}
	return nil
}
