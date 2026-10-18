package asr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"voice_input/internal/audio"
	"voice_input/internal/netapi"
	"voice_input/internal/settings"
)

const mimoDataPrefix = "data:audio/wav;base64,"
const mimoMaxDataBytes = 10_000_000

type MiMo struct {
	Client *http.Client
	Config settings.ASR
	Key    string
}

func mimoLanguage(language string) bool {
	return language == "" || language == "auto" || language == "zh" || language == "en"
}

func (p MiMo) Transcribe(ctx context.Context, data audio.Data) (string, error) {
	return p.transcribe(ctx, data, false)
}

// Probe verifies the protocol using synthetic silence, not microphone audio.
func (p MiMo) Probe(ctx context.Context) error {
	_, err := p.transcribe(ctx, audio.Data{WAV: audio.WAV(make([]float32, 8000))}, true)
	return err
}

func (p MiMo) transcribe(ctx context.Context, data audio.Data, allowEmpty bool) (string, error) {
	if netapi.Endpoint(p.Config.Endpoint) != nil || strings.TrimSpace(p.Config.Model) == "" || len(p.Config.Model) > 200 || strings.ContainsAny(p.Config.Model, "\r\n") || settings.ValidKey(p.Key) != nil || !mimoLanguage(p.Config.Language) {
		return "", netapi.Fail("asr", "config")
	}
	if err := ctx.Err(); err != nil {
		code := "cancelled"
		if err == context.DeadlineExceeded {
			code = "timeout"
		}
		return "", netapi.Fail("asr", code)
	}
	// Include the data URL prefix in the conservative decimal 10 MB limit.
	if len(data.WAV) > audio.MaxWAVBytes || base64.StdEncoding.EncodedLen(len(data.WAV))+len(mimoDataPrefix) > mimoMaxDataBytes {
		return "", netapi.Fail("asr", "upload_large")
	}
	if len(data.WAV) < 44 || string(data.WAV[:4]) != "RIFF" || string(data.WAV[8:12]) != "WAVE" {
		return "", audio.ErrQuiet
	}
	payload := map[string]any{
		"model":  p.Config.Model,
		"stream": false,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "input_audio",
				"input_audio": map[string]string{"data": mimoDataPrefix + base64.StdEncoding.EncodeToString(data.WAV)},
			}},
		}},
	}
	if p.Config.Language != "" {
		payload["asr_options"] = map[string]string{"language": p.Config.Language}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", netapi.Fail("asr", "request")
	}
	req, err := http.NewRequest(http.MethodPost, p.Config.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", netapi.Fail("asr", "config")
	}
	req.Header.Set("Authorization", "Bearer "+p.Key)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(ctx, netapi.Timeout(p.Config.TimeoutSeconds))
	defer cancel()
	response, err := netapi.Do(ctx, p.Client, req, "asr", 1<<20)
	if err != nil {
		return "", err
	}
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role         string          `json:"role"`
				Content      *string         `json:"content"`
				ToolCalls    json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage `json:"function_call"`
				Refusal      json.RawMessage `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if netapi.CheckJSON(response) != nil || json.Unmarshal(response, &result) != nil || len(result.Choices) != 1 {
		return "", netapi.Fail("asr", "response")
	}
	choice := result.Choices[0]
	message := choice.Message
	if choice.FinishReason != "stop" || message.Role != "assistant" || message.Content == nil || len(*message.Content) > 32000 || strings.ContainsRune(*message.Content, 0) || (!allowEmpty && strings.TrimSpace(*message.Content) == "") {
		return "", netapi.Fail("asr", "response")
	}
	for _, extra := range []json.RawMessage{message.ToolCalls, message.FunctionCall, message.Refusal} {
		if len(extra) != 0 && !bytes.Equal(bytes.TrimSpace(extra), []byte("null")) {
			return "", netapi.Fail("asr", "response")
		}
	}
	return *message.Content, nil
}
