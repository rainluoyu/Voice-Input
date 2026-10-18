package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyProfileMigratesWithoutRewritingOnOpen(t *testing.T) {
	cfg := Default()
	cfg.Profile = Profile{Name: "小岚", Nickname: "岚", City: "上海", Timezone: "Asia/Shanghai", CustomBackground: "保留项目名 VoicePad\n偏好简洁表达"}
	cfg.Popup.AlwaysOnTop = false
	original, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "姓名：小岚\n常用称呼：岚\n城市：上海\n时区：Asia/Shanghai\n保留项目名 VoicePad\n偏好简洁表达"
	loaded := store.Get()
	if loaded.Profile != (Profile{CustomBackground: want}) || !loaded.Popup.AlwaysOnTop {
		t.Fatalf("migration: %+v", loaded.Profile)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("opening a config rewrote it")
	}
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	var wire struct {
		Profile map[string]any `json:"profile"`
	}
	if err := json.Unmarshal(saved, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Profile) != 1 || wire.Profile["custom_background"] != want {
		t.Fatal("legacy fields survived save")
	}
	loaded.Profile.CustomBackground = ""
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil || reopened.Get().Profile.Text() != "" {
		t.Fatal("cleared background restored legacy fields")
	}
}

func TestLargeFreeBackgroundRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	cfg.Profile.CustomBackground = strings.Repeat("📦", 16000)
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil || reopened.Get().Profile.CustomBackground != cfg.Profile.CustomBackground {
		t.Fatal("large background did not roundtrip", err)
	}
	cfg.Profile.CustomBackground += "字"
	if cfg.Validate() == nil {
		t.Fatal("oversized background accepted")
	}
	cfg.Profile.CustomBackground = "bad\x00value"
	if cfg.Validate() == nil {
		t.Fatal("NUL background accepted")
	}
}
