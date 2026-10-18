package search

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"voice_input/internal/netapi"
	"voice_input/internal/settings"
)

const Endpoint = "https://api.exa.ai/search"

type Hit struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"published_date,omitempty"`
	Highlights    []string `json:"highlights"`
}
type Provider interface {
	Search(context.Context, string) ([]Hit, error)
}
type Exa struct {
	Client   *http.Client
	Key      string
	Config   settings.Exa
	endpoint string
}

func (e Exa) Search(ctx context.Context, query string) ([]Hit, error) {
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 500 || !utf8.ValidString(query) {
		return nil, netapi.Fail("exa", "request")
	}
	if settings.ValidKey(e.Key) != nil {
		return nil, netapi.Fail("exa", "config")
	}
	if e.Config.SearchType != "instant" && e.Config.SearchType != "fast" {
		return nil, netapi.Fail("exa", "config")
	}
	payload := struct {
		Query      string          `json:"query"`
		Type       string          `json:"type"`
		NumResults int             `json:"numResults"`
		Contents   map[string]bool `json:"contents"`
	}{query, e.Config.SearchType, 3, map[string]bool{"highlights": true}}
	body, _ := json.Marshal(payload)
	endpoint := Endpoint
	if e.endpoint != "" {
		endpoint = e.endpoint
	} // test-only, never exposed in user config
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, netapi.Fail("exa", "config")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", e.Key)
	ctx, cancel := context.WithTimeout(ctx, netapi.Timeout(e.Config.TimeoutSeconds))
	defer cancel()
	data, err := netapi.Do(ctx, e.Client, req, "exa", 2<<20)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Results *[]struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			PublishedDate string   `json:"publishedDate"`
			Highlights    []string `json:"highlights"`
		} `json:"results"`
	}
	if netapi.CheckJSON(data) != nil || json.Unmarshal(data, &wire) != nil || wire.Results == nil {
		return nil, netapi.Fail("exa", "response")
	}
	result := make([]Hit, 0, 3)
	for _, r := range *wire.Results {
		u, err := url.Parse(r.URL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || len(r.URL) > 2000 {
			continue
		}
		hit := Hit{Title: trim(r.Title, 200), URL: r.URL, PublishedDate: trim(r.PublishedDate, 60), Highlights: []string{}}
		for _, text := range r.Highlights {
			if len(hit.Highlights) == 2 {
				break
			}
			hit.Highlights = append(hit.Highlights, trim(text, 600))
		}
		result = append(result, hit)
		if len(result) == 3 {
			break
		}
	}
	return result, nil
}
func trim(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
