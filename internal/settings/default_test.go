package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePortableFixture(t *testing.T, path string, cfg Config) []byte {
	t.Helper()
	data, err := configJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func assertNoConfigTemps(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, ".config-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary config left behind", files, err)
	}
}

func TestConfigPathUsesExecutableDirectory(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "应用 with spaces", "renamed.exe")
	path, err := defaultPath(func() (string, error) { return exe, nil })
	if err != nil || path != filepath.Join(filepath.Dir(exe), "config.json") {
		t.Fatal(path, err)
	}
	for _, raw := range []string{"", "voice_input.exe", filepath.Join("relative", "voice_input.exe")} {
		if _, err := defaultPath(func() (string, error) { return raw, nil }); err == nil {
			t.Fatal("relative executable would depend on cwd", raw)
		}
	}
	if _, err := defaultPath(func() (string, error) { return exe, errors.New("fixture") }); err == nil {
		t.Fatal("executable lookup failure ignored")
	}
}

func TestLocalConfigWinsWithoutLegacyLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.Profile.CustomBackground = "本地设置"
	writePortableFixture(t, path, cfg)
	store, err := openWithLegacy(path, func() (string, error) {
		t.Fatal("existing local config must not inspect user config directory")
		return "", errors.New("unavailable")
	})
	if err != nil || store.path != path || store.Get() != cfg {
		t.Fatal("local config not selected", err)
	}
}

func TestInvalidLocalEntryNeverFallsBack(t *testing.T) {
	for _, kind := range []string{"invalid", "directory", "dangling-link"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			switch kind {
			case "invalid":
				if err := os.WriteFile(path, []byte(`{"version":999}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "dangling-link":
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			store, err := openWithLegacy(path, func() (string, error) {
				t.Fatal("invalid local entry must not trigger migration")
				return "", nil
			})
			if err == nil || store != nil {
				t.Fatal("invalid local entry accepted")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("local entry was replaced", err)
			}
		})
	}
}

func TestMissingConfigsStayUnwrittenUntilSave(t *testing.T) {
	root := t.TempDir()
	path, legacy := filepath.Join(root, "config.json"), filepath.Join(root, "old", "config.json")
	store, err := openWithLegacy(path, func() (string, error) { return legacy, nil })
	if err != nil || store.path != path || store.Get() != Default() {
		t.Fatal("missing configs did not produce portable defaults", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("opening defaults wrote a file", err)
	}
	if err := store.Save(store.Get()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("save did not create local config", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("save wrote legacy path", err)
	}
	assertNoConfigTemps(t, root)
}

func TestMissingLocalCannotIgnoreLegacyLookupFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if store, err := openWithLegacy(path, func() (string, error) { return "", errors.New("unavailable") }); err == nil || store != nil {
		t.Fatal("legacy lookup failure silently reset settings")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed lookup wrote config", err)
	}
}

func TestLegacyConfigMigratesWithoutChangingSource(t *testing.T) {
	root := t.TempDir()
	path, legacy := filepath.Join(root, "app", "config.json"), filepath.Join(root, "old", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"version":1,"mode":"mock","hotkey":"Ctrl+Alt+A","llm":{"dictation_prompt":"保留轻松语气😀","dialogue_prompt":"直接完成请求📦","max_tool_rounds":0},"exa":{"max_calls":8},"profile":{"name":"原称呼","custom_background":"背景和术语"}}`)
	if err := os.WriteFile(legacy, before, 0600); err != nil {
		t.Fatal(err)
	}
	original, err := Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openWithLegacy(path, func() (string, error) { return legacy, nil })
	if err != nil || store.path != path || store.Get() != original.Get() {
		t.Fatal("migration lost config values", err)
	}
	cfg := store.Get()
	if cfg.Mode != "live" || cfg.AIHotkey != "Ctrl+Alt+Shift+A" || cfg.LLM.MaxToolRounds != 0 || cfg.Exa.MaxCalls != 8 || cfg.Profile.Name != "" || !strings.Contains(cfg.Profile.CustomBackground, "原称呼") {
		t.Fatal("legacy defaults or normalization were lost")
	}
	reopened, err := Open(path)
	if err != nil || reopened.Get() != cfg {
		t.Fatal("migrated config cannot reopen", err)
	}
	cfg.Profile.CustomBackground = "只更新程序目录"
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(legacy)
	if err != nil || string(after) != string(before) {
		t.Fatal("legacy source changed", err)
	}
	reopened, err = Open(path)
	if err != nil || reopened.Get() != cfg {
		t.Fatal("subsequent save did not use local path", err)
	}
	assertNoConfigTemps(t, filepath.Dir(path))
}

func TestInvalidLegacyDoesNotCreateLocal(t *testing.T) {
	for _, data := range []string{`{"version":999}`, `{"extra":true}`, "{}" + strings.Repeat(" ", maxConfigBytes)} {
		root := t.TempDir()
		path, legacy := filepath.Join(root, "local.json"), filepath.Join(root, "old.json")
		if err := os.WriteFile(legacy, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if store, err := openWithLegacy(path, func() (string, error) { return legacy, nil }); err == nil || store != nil {
			t.Fatal("invalid legacy source accepted")
		}
		after, err := os.ReadFile(legacy)
		if err != nil || string(after) != data {
			t.Fatal("invalid legacy changed", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid migration created local", err)
		}
		assertNoConfigTemps(t, root)
	}
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := open(path, false); err == nil {
		t.Fatal("disappeared source must not be mistaken for defaults")
	}
	if store, err := Open(path); err != nil || store.Get() != Default() {
		t.Fatal("explicit Open missing-path contract changed", err)
	}
}

func TestLocalCreatedDuringMigrationWins(t *testing.T) {
	for _, kind := range []string{"valid", "invalid", "directory", "dangling-link", "lookup-fails", "legacy-invalid", "legacy-missing"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path, legacy := filepath.Join(root, "config.json"), filepath.Join(root, "old.json")
			old, current := Default(), Default()
			old.Profile.CustomBackground, current.Profile.CustomBackground = "旧配置", "竞争者创建的配置"
			writePortableFixture(t, legacy, old)
			var localBytes []byte
			store, err := openWithLegacy(path, func() (string, error) {
				switch kind {
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "dangling-link":
					if err := os.Symlink(filepath.Join(root, "missing"), path); err != nil {
						t.Skip("symlinks unavailable", err)
					}
				case "invalid":
					localBytes = []byte(`{"version":999}`)
					if err := os.WriteFile(path, localBytes, 0600); err != nil {
						t.Fatal(err)
					}
				default:
					localBytes = writePortableFixture(t, path, current)
				}
				switch kind {
				case "lookup-fails":
					return "", errors.New("lookup failed after local appeared")
				case "legacy-invalid":
					if err := os.WriteFile(legacy, []byte(`{"version":999}`), 0600); err != nil {
						t.Fatal(err)
					}
				case "legacy-missing":
					if err := os.Remove(legacy); err != nil {
						t.Fatal(err)
					}
				}
				return legacy, nil
			})
			bad := kind == "invalid" || kind == "directory" || kind == "dangling-link"
			if bad {
				if err == nil || store != nil {
					t.Fatal("invalid race target accepted")
				}
			} else if err != nil || store.path != path || store.Get() != current {
				t.Fatal("local race winner not selected", err)
			}
			if localBytes != nil {
				after, readErr := os.ReadFile(path)
				if readErr != nil || string(after) != string(localBytes) {
					t.Fatal("race target overwritten", readErr)
				}
			}
			assertNoConfigTemps(t, root)
		})
	}
}

func TestMigrationPublishCannotOverwriteAndFailureKeepsState(t *testing.T) {
	for _, kind := range []string{"publish-failure", "race-file", "race-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.json")
			store := &Store{path: path, config: Default()}
			original := store.Get()
			updated := original
			updated.Profile.CustomBackground = "迁移的值"
			var winner []byte
			err := store.saveWithPublish(updated, func(temp, target string) error {
				ready, err := Open(temp)
				if err != nil || ready.Get() != updated {
					t.Fatal("partial config reached publisher", err)
				}
				if store.config != original {
					t.Fatal("memory changed before publication")
				}
				switch kind {
				case "publish-failure":
					return errors.New("fixture publication failure")
				case "race-file":
					cfg := Default()
					cfg.Profile.CustomBackground = "竞争者"
					winner = writePortableFixture(t, target, cfg)
				case "race-directory":
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				}
				return publishNoReplace(temp, target)
			})
			if err == nil || store.Get() != original {
				t.Fatal("publication failure changed memory", err)
			}
			if kind == "publish-failure" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("failed publication left target", err)
				}
			} else if winner != nil {
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(winner) {
					t.Fatal("winner replaced", err)
				}
			}
			assertNoConfigTemps(t, root)
		})
	}
}

func TestNonFileLegacyDoesNotCreateLocal(t *testing.T) {
	for _, kind := range []string{"directory", "dangling-link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path, legacy := filepath.Join(root, "config.json"), filepath.Join(root, "old.json")
			if kind == "directory" {
				if err := os.Mkdir(legacy, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(root, "missing"), legacy); err != nil {
				t.Skip("symlinks unavailable", err)
			}
			before, err := os.Lstat(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if store, err := openWithLegacy(path, func() (string, error) { return legacy, nil }); err == nil || store != nil {
				t.Fatal("non-file legacy source accepted")
			}
			after, err := os.Lstat(legacy)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("legacy entry changed", err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("non-file legacy source created config", err)
			}
			assertNoConfigTemps(t, root)
		})
	}
}

func TestMigrationToUnwritableDirectoryPreservesLegacy(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app")
	path, legacy := filepath.Join(dir, "config.json"), filepath.Join(root, "old.json")
	before := writePortableFixture(t, legacy, Default())
	if err := os.Mkdir(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	probe, err := os.CreateTemp(dir, "permission-probe-*")
	if err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		t.Skip("filesystem or user bypasses directory write permissions")
	}
	store, err := openWithLegacy(path, func() (string, error) { return legacy, nil })
	if err == nil || store != nil || !strings.Contains(err.Error(), "可写目录") {
		t.Fatal("migration must report an unwritable executable directory", err)
	}
	after, err := os.ReadFile(legacy)
	if err != nil || string(after) != string(before) {
		t.Fatal("failed migration changed source", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("failed migration left local config", err)
	}
	assertNoConfigTemps(t, dir)
}

func TestUnreadableConfigDoesNotResetOrMigrate(t *testing.T) {
	for _, local := range []bool{true, false} {
		t.Run(map[bool]string{true: "local", false: "legacy"}[local], func(t *testing.T) {
			root := t.TempDir()
			path, legacy := filepath.Join(root, "config.json"), filepath.Join(root, "old.json")
			blocked := legacy
			if local {
				blocked = path
			}
			before := writePortableFixture(t, blocked, Default())
			if err := os.Chmod(blocked, 0); err != nil {
				t.Skip("mode permissions unavailable", err)
			}
			t.Cleanup(func() { _ = os.Chmod(blocked, 0600) })
			if _, err := os.ReadFile(blocked); err == nil {
				t.Skip("filesystem or user bypasses mode read permissions")
			}
			store, err := openWithLegacy(path, func() (string, error) {
				if local {
					t.Fatal("unreadable local must not inspect legacy")
				}
				return legacy, nil
			})
			if err == nil || store != nil {
				t.Fatal("unreadable config accepted")
			}
			if err := os.Chmod(blocked, 0600); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(blocked)
			if err != nil || string(after) != string(before) {
				t.Fatal("unreadable source changed", err)
			}
			if !local {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("failed migration wrote local", err)
				}
			}
			assertNoConfigTemps(t, root)
		})
	}
}
