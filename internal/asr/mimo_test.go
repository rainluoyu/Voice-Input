package asr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"voice_input/internal/audio"
	"voice_input/internal/netapi"
	"voice_input/internal/settings"
)

func mimoCompletion(text string) string {
	body, _ := json.Marshal(map[string]any{
		"id": "fixture", "object": "chat.completion", "model": "mimo-v2.5-asr",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": text}}},
		"usage":   map[string]int{"total_tokens": 1},
	})
	return string(body)
}

func testMiMo(t *testing.T, handler http.HandlerFunc) MiMo {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return MiMo{Client: server.Client(), Config: settings.ASR{Provider: "mimo", Endpoint: server.URL + "/v1/chat/completions", Model: "mimo-v2.5-asr", TimeoutSeconds: 5}, Key: "secret-test-key"}
}

func TestMiMoPayloadAndLanguage(t *testing.T) {
	wav := audio.WAV([]float32{0.5, -0.25, 0, 1})
	for _, language := range []string{"", "auto", "zh", "en"} {
		t.Run("language_"+language, func(t *testing.T) {
			p := testMiMo(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-test-key" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("wrong method, endpoint or headers")
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				dataURL := mimoDataPrefix + base64.StdEncoding.EncodeToString(wav)
				expected := map[string]any{
					"model": "mimo-v2.5-asr", "stream": false,
					"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": dataURL}}}}},
				}
				if language != "" {
					expected["asr_options"] = map[string]any{"language": language}
				}
				if !reflect.DeepEqual(payload, expected) {
					t.Error("payload differs from the audio-only non-streaming contract")
				}
				// Decode the transmitted bytes separately from the expected payload.
				messages, _ := payload["messages"].([]any)
				if len(messages) == 1 {
					message, _ := messages[0].(map[string]any)
					parts, _ := message["content"].([]any)
					if len(parts) == 1 {
						part, _ := parts[0].(map[string]any)
						input, _ := part["input_audio"].(map[string]any)
						encoded, _ := input["data"].(string)
						decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, mimoDataPrefix))
						if err != nil || !bytes.Equal(decoded, wav) {
							t.Error("base64 upload changed the original WAV")
						}
					}
				}
				_, _ = io.WriteString(w, mimoCompletion("中文转录 📦"))
			})
			p.Config.Language = language
			text, err := p.Transcribe(context.Background(), audio.Data{WAV: wav})
			if err != nil || text != "中文转录 📦" {
				t.Fatalf("transcribe: %q, %v", text, err)
			}
		})
	}
}

func TestMiMoResponseValidation(t *testing.T) {
	valid := mimoCompletion("文字")
	for _, tt := range []struct {
		name, body   string
		probe, valid bool
	}{
		{"valid", valid, false, true},
		{"null_optional_fields", strings.Replace(valid, `"role":"assistant"`, `"role":"assistant","tool_calls":null,"function_call":null,"refusal":null`, 1), false, true},
		{"probe_empty", mimoCompletion(""), true, true},
		{"transcribe_empty", mimoCompletion(""), false, false},
		{"transcribe_whitespace", mimoCompletion(" \n\t"), false, false},
		{"missing_content", `{"choices":[{"finish_reason":"stop","message":{"role":"assistant"}}]}`, true, false},
		{"null_content", `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null}}]}`, true, false},
		{"array_content", `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[]}}]}`, true, false},
		{"missing_role", strings.Replace(valid, `,"role":"assistant"`, "", 1), false, false},
		{"wrong_role", strings.Replace(valid, `"assistant"`, `"user"`, 1), false, false},
		{"missing_finish", strings.Replace(valid, `"finish_reason":"stop",`, "", 1), false, false},
		{"truncated", strings.Replace(valid, `"stop"`, `"length"`, 1), false, false},
		{"tool_finish", strings.Replace(valid, `"stop"`, `"tool_calls"`, 1), false, false},
		{"filtered", strings.Replace(valid, `"stop"`, `"content_filter"`, 1), false, false},
		{"tool_calls", strings.Replace(valid, `"role":"assistant"`, `"role":"assistant","tool_calls":[{"id":"tool"}]`, 1), true, false},
		{"empty_tool_calls", strings.Replace(valid, `"role":"assistant"`, `"role":"assistant","tool_calls":[]`, 1), true, false},
		{"function_call", strings.Replace(valid, `"role":"assistant"`, `"role":"assistant","function_call":{}`, 1), true, false},
		{"refusal", strings.Replace(valid, `"role":"assistant"`, `"role":"assistant","refusal":"no"`, 1), true, false},
		{"no_choices", `{"choices":[]}`, true, false},
		{"multiple_choices", `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"one"}},{"finish_reason":"stop","message":{"role":"assistant","content":"two"}}]}`, true, false},
		{"duplicate_top_field", strings.Replace(valid, `"choices":`, `"choices":[],"choices":`, 1), false, false},
		{"duplicate_content", strings.Replace(valid, `"content":`, `"content":"hidden","content":`, 1), true, false},
		{"trailing_json", valid + `{}`, false, false},
		{"bad_json", `not json`, true, false},
		{"invalid_utf8", strings.Replace(valid, "文字", string([]byte{0xff}), 1), false, false},
		{"nul", mimoCompletion("text\x00"), false, false},
		{"text_limit", mimoCompletion(strings.Repeat("x", 32000)), false, true},
		{"text_over_limit", mimoCompletion(strings.Repeat("x", 32001)), true, false},
		{"multibyte_limit", mimoCompletion(strings.Repeat("中", 10666)), false, true},
		{"multibyte_over_limit", mimoCompletion(strings.Repeat("中", 10667)), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testMiMo(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tt.body) })
			var err error
			if tt.probe {
				err = p.Probe(context.Background())
			} else {
				_, err = p.Transcribe(context.Background(), audio.Data{WAV: audio.WAV(nil)})
			}
			if tt.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertASRError(t, err, "response")
			}
		})
	}
}

func TestMiMoProbeUploadsIndependentSilence(t *testing.T) {
	p := testMiMo(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content []struct {
					InputAudio struct {
						Data string `json:"data"`
					} `json:"input_audio"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 1 || len(body.Messages[0].Content) != 1 {
			t.Error("invalid probe request")
			w.WriteHeader(400)
			return
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(body.Messages[0].Content[0].InputAudio.Data, mimoDataPrefix))
		if err != nil || !bytes.Equal(data, audio.WAV(make([]float32, 8000))) {
			t.Error("probe must upload only fresh synthetic silence")
		}
		_, _ = io.WriteString(w, mimoCompletion(""))
	})
	if err := p.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMiMoUploadLimitBeforeRequest(t *testing.T) {
	// A base64 quantum has four bytes, so the nearest accepted size is below
	// 10,000,000 after including the 21-byte data URL prefix.
	maxRaw := ((mimoMaxDataBytes - len(mimoDataPrefix)) / 4) * 3
	for _, size := range []int{maxRaw, maxRaw + 1, audio.MaxWAVBytes, audio.MaxWAVBytes + 1} {
		t.Run("bytes_"+strconv.Itoa(size), func(t *testing.T) {
			var calls atomic.Int32
			p := testMiMo(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, mimoCompletion("ok"))
			})
			wav := make([]byte, size)
			copy(wav, audio.WAV(nil))
			_, err := p.Transcribe(context.Background(), audio.Data{WAV: wav})
			if size == maxRaw {
				if err != nil || calls.Load() != 1 {
					t.Fatalf("limit boundary rejected: %v", err)
				}
			} else {
				assertASRError(t, err, "upload_large")
				if calls.Load() != 0 || !strings.Contains(err.Error(), "上传音频过大") || strings.Contains(err.Error(), "响应") {
					t.Fatal("oversized upload sent or misreported")
				}
			}
		})
	}
}

func TestMiMoSafeErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"auth", 401, "secret-test-key secret-response", "auth"},
		{"forbidden", 403, "secret-response", "auth"},
		{"rate", 429, "secret-response", "rate"},
		{"server", 503, "secret-response", "server"},
		{"request", 400, "secret-response", "request"},
		{"bad_json", 200, "secret-response", "response"},
		{"response_limit", 200, strings.Repeat("x", (1<<20)+1), "large"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testMiMo(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := p.Transcribe(context.Background(), audio.Data{WAV: audio.WAV(nil)})
			assertASRError(t, err, tt.code)
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), p.Config.Endpoint) || strings.Contains(netapi.SafeMessage(err, "fallback"), "secret") {
				t.Fatal("unsafe diagnostic")
			}
		})
	}
}

type mimoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mimoRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMiMoCancellationTimeoutAndNetwork(t *testing.T) {
	for _, name := range []string{"pre_cancelled", "cancel_in_flight", "parent_timeout", "provider_timeout", "network"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "pre_cancelled" {
				cancel()
			}
			if name == "parent_timeout" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer deadlineCancel()
			}
			p := MiMo{Config: settings.ASR{Endpoint: "https://asr.example/secret-endpoint", Model: "mimo-v2.5-asr", TimeoutSeconds: 5}, Key: "secret-test-key"}
			p.Client = &http.Client{Transport: mimoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				if name == "network" {
					return nil, errors.New("secret-transport-message")
				}
				if name == "cancel_in_flight" {
					cancel()
				}
				deadline, ok := req.Context().Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("provider timeout missing")
				}
				<-req.Context().Done()
				return nil, errors.New("secret-transport-message")
			})}
			if name == "provider_timeout" {
				p.Config.TimeoutSeconds = 0
			}
			_, err := p.Transcribe(ctx, audio.Data{WAV: audio.WAV(nil)})
			code := "cancelled"
			if name == "parent_timeout" || name == "provider_timeout" {
				code = "timeout"
			}
			if name == "network" {
				code = "network"
			}
			assertASRError(t, err, code)
			if name == "pre_cancelled" && calls.Load() != 0 {
				t.Fatal("cancelled request sent")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("transport details leaked")
			}
		})
	}
}

func TestMiMoInvalidInputsDoNotSend(t *testing.T) {
	var calls atomic.Int32
	p := testMiMo(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, wav := range [][]byte{nil, make([]byte, 43), make([]byte, 44), append([]byte("RIFF"), make([]byte, 40)...)} {
		if _, err := p.Transcribe(context.Background(), audio.Data{WAV: wav}); !errors.Is(err, audio.ErrQuiet) {
			t.Fatalf("invalid WAV accepted: %v", err)
		}
	}
	for _, language := range []string{"ja", "ZH", "zh-CN", "zh\r\nsecret"} {
		p.Config.Language = language
		_, err := p.Transcribe(context.Background(), audio.Data{WAV: audio.WAV(nil)})
		assertASRError(t, err, "config")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent")
	}
}
