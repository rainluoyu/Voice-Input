package settings

import (
	"os"
	"path/filepath"

	"voice_input/internal/i18n"
)

func DefaultPath() (string, error) { return defaultPath(os.Executable) }

func defaultPath(executable func() (string, error)) (string, error) {
	path, err := executable()
	if err != nil || !filepath.IsAbs(path) {
		return "", i18n.Error("settings.executable")
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
		return nil, false, i18n.Error("settings.local_stat")
	}
	store, err := open(path, false)
	if err != nil {
		return nil, true, i18n.Wrap(err, "settings.local_read")
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
		return localOrError(path, i18n.Error("settings.legacy_dir"))
	}
	if _, err := os.Lstat(legacy); os.IsNotExist(err) {
		// Another session may have created the local entry during the lookup.
		return localOrDefault(path)
	} else if err != nil {
		return localOrError(path, i18n.Error("settings.legacy_stat"))
	}
	previous, err := open(legacy, false)
	if err != nil {
		return localOrError(path, i18n.Wrap(err, "settings.legacy_invalid"))
	}
	store := &Store{path: path, config: Default()}
	if err := store.saveWithPublish(previous.Get(), publishNoReplace); err != nil {
		// Do not assume every filesystem reports an existing target the same way.
		return localOrError(path, i18n.Wrap(err, "settings.legacy_migrate"))
	}
	return store, nil
}
