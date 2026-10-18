//go:build windows

package i18n

import "golang.org/x/sys/windows"

func SystemLocale() Locale {
	languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return EN
	}
	return Match(languages)
}
