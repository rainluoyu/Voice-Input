//go:build !windows

package netapi

import (
	"net/http"
	"net/url"
)

func systemProxy() func(*http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment
}
