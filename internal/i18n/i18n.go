package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strings"
)

type Locale string

const (
	EN   Locale = "en"
	ZhCN Locale = "zh-CN"
)

// Match selects the first supported language in preference order. Chinese
// variants share the simplified dictionary; unsupported languages fall back to English.
func Match(languages []string) Locale {
	for _, language := range languages {
		tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
		if tag == "zh" || strings.HasPrefix(tag, "zh-") {
			return ZhCN
		}
		if tag == "en" || strings.HasPrefix(tag, "en-") {
			return EN
		}
	}
	return EN
}

func Resolve(preference string) Locale {
	switch preference {
	case string(EN):
		return EN
	case string(ZhCN):
		return ZhCN
	default:
		return SystemLocale()
	}
}

type Message struct {
	Code string            `json:"code"`
	Args map[string]string `json:"args,omitempty"`
}

// M accepts named argument pairs. An unmatched final name is ignored.
func M(code string, args ...string) Message {
	m := Message{Code: code}
	if len(args) >= 2 {
		m.Args = make(map[string]string, len(args)/2)
		for i := 0; i+1 < len(args); i += 2 {
			m.Args[args[i]] = args[i+1]
		}
	}
	return m
}

func (m Message) Text(locale Locale) string {
	if m.Code == "" {
		return ""
	}
	if locale != ZhCN {
		locale = EN
	}
	text := catalog[locale][m.Code]
	keys := make([]string, 0, len(m.Args))
	for key := range m.Args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	replacements := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		replacements = append(replacements, "{"+key+"}", m.Args[key])
	}
	if len(replacements) != 0 {
		text = strings.NewReplacer(replacements...).Replace(text)
	}
	return text
}

// Descriptor is implemented only by errors carrying explicit application metadata.
// Error strings are never inspected or used as translation keys.
type Descriptor interface{ I18nMessage() Message }

func FromError(err error, fallbackCode string) Message {
	var descriptor Descriptor
	if errors.As(err, &descriptor) {
		m := descriptor.I18nMessage()
		if m.Code != "" && catalog[EN][m.Code] != "" {
			return m
		}
	}
	return M(fallbackCode)
}

type messageError struct {
	message Message
	cause   error
}

func (e *messageError) Error() string        { return e.message.Text(ZhCN) }
func (e *messageError) I18nMessage() Message { return e.message }
func (e *messageError) Unwrap() error        { return e.cause }

func Error(code string, args ...string) error { return Wrap(nil, code, args...) }
func Wrap(err error, code string, args ...string) error {
	return &messageError{message: M(code, args...), cause: err}
}

//go:embed *.json
var dictionaries embed.FS

var catalog = loadCatalog(dictionaries)

func loadCatalog(files fs.FS) map[Locale]map[string]string {
	result := map[Locale]map[string]string{EN: {}, ZhCN: {}}
	entries, err := fs.Glob(files, "*.json")
	if err != nil {
		panic(err)
	}
	for _, name := range entries {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			panic(err)
		}
		var dictionary map[Locale]map[string]string
		if err := json.Unmarshal(data, &dictionary); err != nil {
			panic(err)
		}
		for locale, messages := range dictionary {
			if result[locale] == nil {
				panic("unsupported dictionary locale: " + string(locale))
			}
			for key, text := range messages {
				if _, duplicate := result[locale][key]; duplicate {
					panic("duplicate dictionary key: " + key)
				}
				result[locale][key] = text
			}
		}
	}
	return result
}
