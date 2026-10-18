package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"

	"voice_input/internal/audio"
	"voice_input/internal/netapi"
	"voice_input/internal/settings"
)

type Provider interface {
	Transcribe(context.Context, audio.Data) (string, error)
}
type Compatible struct {
	Client *http.Client
	Config settings.ASR
	Key    string
}

func (p Compatible) Transcribe(ctx context.Context, data audio.Data) (string, error) {
	return p.transcribe(ctx, data, false)
}

// Probe uses synthetic silence and permits an empty transcript: it verifies
// transport/protocol, not speech recognition accuracy, without recording a user.
func (p Compatible) Probe(ctx context.Context) error {
	_, err := p.transcribe(ctx, audio.Data{WAV: audio.WAV(make([]float32, 8000))}, true)
	return err
}
func (p Compatible) transcribe(ctx context.Context, data audio.Data, allowEmpty bool) (string, error) {
	if netapi.Endpoint(p.Config.Endpoint) != nil || p.Config.Model == "" || settings.ValidKey(p.Key) != nil {
		return "", netapi.Fail("asr", "config")
	}
	if len(data.WAV) < 44 || len(data.WAV) > audio.MaxWAVBytes || string(data.WAV[:4]) != "RIFF" || string(data.WAV[8:12]) != "WAVE" {
		return "", audio.ErrQuiet
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", p.Config.Model)
	_ = writer.WriteField("response_format", "json")
	if p.Config.Language != "" {
		_ = writer.WriteField("language", p.Config.Language)
	}
	file, err := writer.CreateFormFile("file", "recording.wav")
	if err != nil {
		return "", netapi.Fail("asr", "request")
	}
	_, _ = file.Write(data.WAV)
	_ = writer.Close()
	req, err := http.NewRequest(http.MethodPost, p.Config.Endpoint, &body)
	if err != nil {
		return "", netapi.Fail("asr", "config")
	}
	req.Header.Set("Authorization", "Bearer "+p.Key)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx, cancel := context.WithTimeout(ctx, netapi.Timeout(p.Config.TimeoutSeconds))
	defer cancel()
	response, err := netapi.Do(ctx, p.Client, req, "asr", 1<<20)
	if err != nil {
		return "", err
	}
	var result struct {
		Text *string `json:"text"`
	}
	if netapi.CheckJSON(response) != nil || json.Unmarshal(response, &result) != nil || result.Text == nil || len(*result.Text) > 32000 || strings.ContainsRune(*result.Text, 0) || (!allowEmpty && strings.TrimSpace(*result.Text) == "") {
		return "", netapi.Fail("asr", "response")
	}
	return *result.Text, nil
}
