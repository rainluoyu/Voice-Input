package i18n

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		tags []string
		want Locale
	}{
		{nil, EN}, {[]string{"fr-FR", "zh-CN"}, ZhCN}, {[]string{"en-GB", "zh-CN"}, EN},
		{[]string{"zh-Hans-SG"}, ZhCN}, {[]string{"zh_SG"}, ZhCN}, {[]string{"zh-TW", "zh-Hant", "ja"}, ZhCN},
		{[]string{"zh"}, ZhCN}, {[]string{"zh-Hant-CN", "en-US"}, ZhCN}, {[]string{"zh-TW", "en-US"}, ZhCN}, {[]string{"zh-HK"}, ZhCN},
	} {
		if got := Match(tc.tags); got != tc.want {
			t.Errorf("Match(%v)=%s, want %s", tc.tags, got, tc.want)
		}
	}
	if Resolve("en") != EN || Resolve("zh-CN") != ZhCN || Resolve("auto") != SystemLocale() || Resolve("") != SystemLocale() {
		t.Fatal("preference resolution failed")
	}
}

func TestDictionariesAgree(t *testing.T) {
	pattern := regexp.MustCompile(`\{[a-zA-Z_][a-zA-Z_0-9]*\}`)
	placeholders := func(text string) []string {
		set := map[string]bool{}
		for _, name := range pattern.FindAllString(text, -1) {
			set[name] = true
		}
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}
	for _, locale := range []Locale{EN, ZhCN} {
		for key, text := range catalog[locale] {
			other, exists := catalog[EN][key]
			if locale == EN {
				other, exists = catalog[ZhCN][key]
			}
			if !exists || text == "" || !reflect.DeepEqual(placeholders(text), placeholders(other)) {
				t.Errorf("dictionary mismatch for %s %s", locale, key)
			}
		}
	}
}

func TestEnglishApplicationCopy(t *testing.T) {
	for key, text := range catalog[EN] {
		if strings.Contains(text, "settings.ui.") {
			t.Errorf("translation key leaked into UI copy: %s", key)
		}
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("untranslated English application copy: %s", key)
				break
			}
		}
	}
}

func TestMultipleDictionariesMerge(t *testing.T) {
	files := fstest.MapFS{
		"one.json": {Data: []byte(`{"en":{"a":"A"},"zh-CN":{"a":"甲"}}`)},
		"two.json": {Data: []byte(`{"en":{"b":"B"},"zh-CN":{"b":"乙"}}`)},
	}
	loaded := loadCatalog(files)
	if loaded[EN]["a"] != "A" || loaded[EN]["b"] != "B" || loaded[ZhCN]["b"] != "乙" {
		t.Fatal("did not merge dictionaries")
	}
}

func TestMessagesAndSafeErrors(t *testing.T) {
	if M("").Text(EN) != "" || M("not_a_key").Text(EN) != "" {
		t.Fatal("empty or unknown code produced guessed text")
	}
	m := M("hotkey.register", "hotkey", "{hotkey} 中文", "dangling")
	if got := m.Text(EN); !strings.Contains(got, "{hotkey} 中文") {
		t.Fatal("argument was changed or recursively interpolated", got)
	}
	secret := errors.New("secret-provider-body")
	if got := FromError(secret, "app.llm_failed"); got.Code != "app.llm_failed" || strings.Contains(got.Text(EN), "secret") {
		t.Fatal("unsafe fallback", got)
	}
	cause := Wrap(secret, "app.copy_stale")
	if !errors.Is(cause, secret) || FromError(fmt.Errorf("wrapped: %w", cause), "app.llm_failed").Code != "app.copy_stale" {
		t.Fatal("descriptor or chain lost")
	}
	if FromError(Error("unknown-secret-key"), "app.llm_failed").Code != "app.llm_failed" {
		t.Fatal("unknown descriptor was exposed")
	}
	if M("app.cancelled").Text(EN) == M("app.cancelled").Text(ZhCN) {
		t.Fatal("locale ignored")
	}
}
