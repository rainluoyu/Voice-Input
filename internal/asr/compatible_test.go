package asr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voice_input/internal/audio"
	"voice_input/internal/settings"
)

func TestMultipartTranscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("wrong protocol")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.FormValue("model") != "asr-model" || r.FormValue("response_format") != "json" || r.FormValue("language") != "zh" {
			t.Error("missing fields")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if header.Filename != "recording.wav" || string(data[:4]) != "RIFF" {
			t.Error("invalid audio file")
		}
		_, _ = w.Write([]byte(`{"text":"中文转录 📦","usage":{"seconds":1}}`))
	}))
	defer server.Close()
	p := Compatible{Client: server.Client(), Config: settings.ASR{Endpoint: server.URL + "/audio/transcriptions", Model: "asr-model", Language: "zh", TimeoutSeconds: 5}, Key: "test-key"}
	text, err := p.Transcribe(context.Background(), audio.Data{WAV: audio.WAV(make([]float32, 16000))})
	if err != nil || text != "中文转录 📦" {
		t.Fatalf("transcribe: %q %v", text, err)
	}
}
func TestFailuresAndProbe(t *testing.T) {
	for _, tt := range []struct {
		status    int
		body      string
		probe     bool
		wantError bool
	}{{401, `secret-key`, false, true}, {429, `quota`, false, true}, {500, `body`, false, true}, {200, `{"text":""}`, false, true}, {200, `{"text":""}`, true, false}, {200, `{"wrong":"text"}`, true, true}, {200, `not json`, false, true}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(tt.body))
		}))
		p := Compatible{Client: server.Client(), Config: settings.ASR{Endpoint: server.URL, Model: "m", TimeoutSeconds: 5}, Key: "secret-key"}
		var err error
		if tt.probe {
			err = p.Probe(context.Background())
		} else {
			_, err = p.Transcribe(context.Background(), audio.Data{WAV: audio.WAV(make([]float32, 16000))})
		}
		server.Close()
		if (err != nil) != tt.wantError {
			t.Fatalf("body %s err %v", tt.body, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret-key") {
			t.Fatal("key leaked")
		}
	}
}
