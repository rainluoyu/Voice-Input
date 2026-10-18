package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func DefaultPath() (string, error) { return defaultPath(os.Executable) }

func defaultPath(executable func() (string, error)) (string, error) {
	path, err := executable()
	if err != nil || !filepath.IsAbs(path) {
		return "", errors.New("无法确定程序所在目录")
	}
	return filepath.Join(filepath.Dir(path), "config.json"), nil
}

func OpenDefault() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return openWithLegacy(path, func() (string, error) {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "voice_input", "config.json"), nil
	})
}

// A local entry always wins, including an unreadable file or a dangling link.
func openLocal(path string) (*Store, bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("无法检查程序目录中的 config.json")
	}
	store, err := open(path, false)
	if err != nil {
		return nil, true, fmt.Errorf("程序目录中的 config.json 无法读取：%w", err)
	}
	return store, true, nil
}

func localOrDefault(path string) (*Store, error) {
	if store, exists, err := openLocal(path); exists || err != nil {
		return store, err
	}
	return &Store{path: path, config: Default()}, nil
}

func localOrError(path string, cause error) (*Store, error) {
	if store, exists, err := openLocal(path); exists || err != nil {
		return store, err
	}
	return nil, cause
}

func openWithLegacy(path string, legacyLookup func() (string, error)) (*Store, error) {
	if store, exists, err := openLocal(path); exists || err != nil {
		return store, err
	}
	legacy, err := legacyLookup()
	if err != nil {
		return localOrError(path, errors.New("无法检查旧用户配置目录；不会覆盖或重置已有配置"))
	}
	if _, err := os.Lstat(legacy); os.IsNotExist(err) {
		// Another session may have created the local entry during the lookup.
		return localOrDefault(path)
	} else if err != nil {
		return localOrError(path, errors.New("无法检查旧配置文件；不会创建新的配置"))
	}
	previous, err := open(legacy, false)
	if err != nil {
		return localOrError(path, fmt.Errorf("旧配置无法迁移，原文件未修改：%w", err))
	}
	store := &Store{path: path, config: Default()}
	if err := store.saveWithPublish(previous.Get(), publishNoReplace); err != nil {
		// Do not assume every filesystem reports an existing target the same way.
		return localOrError(path, fmt.Errorf("旧配置迁移失败，原文件未修改；请检查程序目录权限和可用空间，或移至可写目录：%w", err))
	}
	return store, nil
}
