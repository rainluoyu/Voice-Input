package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/asr"
	"voice_input/internal/audio"
	"voice_input/internal/llm"
	"voice_input/internal/settings"
)

type fakeRecorder struct{}

func (fakeRecorder) Start(context.Context, string, int, func(float64)) error { return nil }
func (fakeRecorder) Stop(context.Context) (audio.Data, error) {
	return audio.Data{WAV: audio.WAV(make([]float32, 16000))}, nil
}

type fakeInjector struct{ count atomic.Int32 }

func (i *fakeInjector) Prepare(ctx context.Context, _ string) (func() error, error) {
	return func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		i.count.Add(1)
		return nil
	}, nil
}
func TestHTTPPipelineWithFakeDesktopAndAudio(t *testing.T) {
	var asrCalls, llmCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asr":
			asrCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "原始转录"})
		case "/v1/chat/completions":
			llmCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": `{"status":"insert","text":"整理结果 📦","notice":""}`}, "finish_reason": "stop"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cfg := settings.Default()
	cfg.Mode = "live"
	cfg.ASR.Endpoint = server.URL + "/asr"
	cfg.ASR.Model = "asr"
	cfg.LLM.BaseURL = server.URL + "/v1"
	cfg.LLM.Model = "llm"
	injector := &fakeInjector{}
	controller := app.NewWithFactory(func(ctx context.Context, id string, intent app.Intent, notify func(app.Stage, float64)) (app.Processor, app.TaskOptions, error) {
		session := &Session{Recorder: fakeRecorder{}, ASR: asr.Compatible{Client: server.Client(), Config: cfg.ASR, Key: "asr-key"}, LLM: llm.Compatible{Client: server.Client(), Config: cfg.LLM, Key: "llm-key"}, Config: cfg, TaskID: id, Intent: intent, Notify: notify}
		return session, app.TaskOptions{Mode: "live", Timeout: time.Second}, nil
	}, injector, time.Hour)
	defer controller.Close()
	controller.Toggle(app.Dialogue)
	controller.Toggle(app.Dialogue)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := controller.Snapshot()
		if s.Stage == app.Done {
			if s.RawText != "原始转录" || s.Text != "整理结果 📦" || injector.count.Load() != 1 || asrCalls.Load() != 1 || llmCalls.Load() != 1 {
				t.Fatalf("bad pipeline: %+v", s)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pipeline did not complete: %+v", controller.Snapshot())
}
func TestASRPassThroughDoesNotCallLLM(t *testing.T) {
	s := Session{Intent: app.Dictation, Config: settings.Config{Mode: "asr_mock_llm"}}
	r, err := s.Complete(context.Background(), "真实 ASR 文本")
	if err != nil || r.Text != "真实 ASR 文本" || r.Status != "insert" {
		t.Fatal("pass-through changed speech")
	}
}

type noKeys struct{}

func (noKeys) Get(string) (string, error) { return "", settings.ErrNoKey }
func (noKeys) Set(string, string) error   { return nil }
func (noKeys) Delete(string) error        { return nil }
func TestUnconfiguredServicesNeverUseFakeResults(t *testing.T) {
	cfg := settings.Default()
	if _, _, err := New(context.Background(), "id", app.Dictation, cfg, noKeys{}, nil); err == nil {
		t.Fatal("unconfigured services accepted")
	}
	cfg = liveConfig()
	if _, _, err := New(context.Background(), "id", app.Dictation, cfg, noKeys{}, nil); err == nil {
		t.Fatal("live without key accepted")
	}
}

type keyVault map[string]string

func (v keyVault) Get(kind string) (string, error) {
	if key := v[kind]; key != "" {
		return key, nil
	}
	return "", settings.ErrNoKey
}
func (v keyVault) Set(kind, key string) error { v[kind] = key; return nil }
func (v keyVault) Delete(kind string) error   { delete(v, kind); return nil }
func liveConfig() settings.Config {
	cfg := settings.Default()
	cfg.Mode = "live"
	cfg.ASR.Endpoint = "https://example.com/v1/audio/transcriptions"
	cfg.ASR.Model = "asr"
	cfg.LLM.BaseURL = "https://example.com/v1"
	cfg.LLM.Model = "llm"
	return cfg
}
func TestSessionFactoryKeyGates(t *testing.T) {
	for _, keys := range []keyVault{{}, {"asr": "test-asr"}, {"asr": "test-asr", "llm": "test-llm"}, {"asr": "test-asr", "llm": "test-llm", "exa": "test-exa"}} {
		ctx, cancel := context.WithCancel(context.Background())
		cfg := liveConfig()
		cfg.Exa.Enabled = true
		p, options, err := New(ctx, "dynamic-task", app.Dialogue, cfg, keys, nil)
		if len(keys) < 3 {
			if err == nil {
				t.Fatal("missing key gate")
			}
		} else {
			if err != nil || options.Mode != "live" {
				t.Fatalf("factory: %v", err)
			}
			session := p.(*Session)
			session.Recorder = fakeRecorder{}
			if err := session.Begin(ctx); err != nil {
				t.Fatal(err)
			}
			adapter := session.LLM.(llm.Compatible)
			if adapter.Search == nil {
				t.Fatal("search permission not connected")
			}
		}
		cancel()
	}
	t.Setenv("PROXY_URL", "unsupported://proxy")
	if _, _, err := New(context.Background(), "task", app.Dictation, liveConfig(), keyVault{"asr": "test-asr", "llm": "test-llm"}, nil); err == nil {
		t.Fatal("invalid proxy accepted")
	}
}

type failingRecorder struct{ fakeRecorder }

func (failingRecorder) Start(context.Context, string, int, func(float64)) error {
	return audio.ErrUnavailable
}
func (failingRecorder) Stop(context.Context) (audio.Data, error) { return audio.Data{}, audio.ErrQuiet }
func TestRecordingFailureIsSafe(t *testing.T) {
	session := Session{Recorder: failingRecorder{}, Config: liveConfig()}
	if session.Begin(context.Background()) == nil {
		t.Fatal("microphone error ignored")
	}
	if _, err := session.Transcribe(context.Background()); err == nil {
		t.Fatal("quiet audio passed to ASR")
	}
}

type capturingLLM struct{ request llm.Request }

func (p *capturingLLM) Complete(_ context.Context, request llm.Request) (llm.Result, error) {
	p.request = request
	return llm.Result{Status: "skip", Notice: "fixture"}, nil
}
func TestRuntimeContextNotHardcoded(t *testing.T) {
	provider := &capturingLLM{}
	cfg := liveConfig()
	cfg.Profile.CustomBackground = "项目名：VoicePad；偏好简洁表达"
	s := Session{Intent: app.Dialogue, Config: cfg, TaskID: "dynamic-id", TargetApp: "current.exe", LLM: provider}
	result, err := s.Complete(context.Background(), "current ASR")
	if err != nil || result.Status != "skip" || provider.request.TaskID != "dynamic-id" || provider.request.TargetApp != "current.exe" || provider.request.ASRText != "current ASR" || provider.request.Background != cfg.Profile.CustomBackground || provider.request.NowLocal.Location() != time.Local || time.Since(provider.request.NowLocal) > time.Second {
		t.Fatal("runtime context incorrect")
	}
}

func TestSessionFactorySelectsASRProvider(t *testing.T) {
	for _, provider := range []string{"", "openai_compatible", "mimo"} {
		t.Run("provider_"+provider, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/complete-asr-endpoint" || r.Header.Get("Authorization") != "Bearer test-asr" {
					t.Error("factory changed endpoint or key")
				}
				if provider == "mimo" {
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "mimo-v2.5-asr" || body["stream"] != false {
						t.Error("wrong MiMo protocol")
					}
					_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"工厂转录"}}]}`))
				} else {
					if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("model") != "mimo-v2.5-asr" {
						t.Error("wrong compatible protocol")
					}
					_, _ = w.Write([]byte(`{"text":"工厂转录"}`))
				}
			}))
			defer server.Close()
			cfg := settings.Default()
			cfg.Mode = "asr_mock_llm"
			cfg.ASR.Provider, cfg.ASR.Endpoint, cfg.ASR.Model = provider, server.URL+"/complete-asr-endpoint", "mimo-v2.5-asr"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			processor, _, err := New(ctx, "factory-task", app.Dictation, cfg, keyVault{"asr": "test-asr"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			session := processor.(*Session)
			switch session.ASR.(type) {
			case asr.MiMo:
				if provider != "mimo" {
					t.Fatal("MiMo selected implicitly")
				}
			case asr.Compatible:
				if provider == "mimo" {
					t.Fatal("MiMo selection lost")
				}
			default:
				t.Fatalf("unexpected ASR adapter %T", session.ASR)
			}
			session.Recorder = fakeRecorder{}
			text, err := session.Transcribe(ctx)
			if err != nil || text != "工厂转录" || calls.Load() != 1 {
				t.Fatalf("factory transcription: %q, %v", text, err)
			}
			result, err := session.Complete(ctx, text)
			if err != nil || result.Text != text || session.LLM != nil {
				t.Fatal("ASR pass-through invoked or changed semantic processing")
			}
		})
	}
	cfg := settings.Default()
	cfg.ASR.Provider = "unknown"
	if _, _, err := New(context.Background(), "task", app.Dictation, cfg, noKeys{}, nil); err == nil {
		t.Fatal("unknown provider bypassed factory validation")
	}
}
