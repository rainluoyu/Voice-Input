package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"voice_input/internal/app"
	"voice_input/internal/asr"
	"voice_input/internal/audio"
	"voice_input/internal/llm"
	"voice_input/internal/netapi"
	"voice_input/internal/search"
	"voice_input/internal/settings"
)

const acceptancePrefix = "VOICE_INPUT_ACCEPTANCE_"
const acceptanceSpeech = "你好，欢迎来到语音工作室，今天我们验证声音与语速。"

// Explicit opt-in only. Subtests are independently selectable, for example:
//
//	VOICE_INPUT_REAL_ACCEPTANCE=1 go test -count=1 -timeout=15m ./internal/pipeline \
//	  -run '^TestRealAcceptance$/^semantics$' -v
//
// Supply a NEW REPORT path for each invocation. No configuration is persisted.
// WAV must be the known, non-private Chinese synthetic fixture, NOT microphone
// audio. ASR_ENDPOINT is complete; LLM_BASE_URL has chat/completions appended.
func TestRealAcceptance(t *testing.T) {
	if os.Getenv("VOICE_INPUT_REAL_ACCEPTANCE") != "1" {
		t.Skip("real services disabled; explicit VOICE_INPUT_REAL_ACCEPTANCE=1 required")
	}
	path := os.Getenv(acceptancePrefix + "REPORT")
	if path == "" {
		t.Fatal("explicit acceptance REPORT path required before any real request")
	}
	// O_EXCL prevents overwriting previous evidence and following an existing symlink.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal("cannot create fresh private acceptance report")
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		t.Fatal("cannot secure acceptance report")
	}
	ledger := &acceptanceLedger{calls: map[string]int{}, elapsed: map[string]int64{}}
	report := &acceptanceReport{
		Version: 1, StartedAt: time.Now(), Pipeline: "deferred_not_selected", Scope: "Only recorded cases were run; unselected cases remain pending. Real network adapters; fixed synthetic inputs; fake recorder/injector. Chinese ASR cases require an explicitly supplied synthetic WAV, not human speech or Windows microphone capture.",
		Windows: "pending: all Windows desktop, microphone, credentials and device acceptance",
		Budget:  map[string]int{"asr": 1, "llm": 10, "exa": 2}, SemanticReview: "pending manual review; automatic assertions do not establish full semantic fidelity or factual entailment",
		Cases: []acceptanceCase{}, file: file, ledger: ledger,
	}
	t.Cleanup(func() {
		report.FinishedAt = time.Now()
		report.Outcome = "passed_automatic_checks_only"
		for _, c := range report.Cases {
			if c.Outcome == "skipped" || c.Outcome == "deferred_missing_configuration" {
				report.Outcome = "partial_skipped"
			}
		}
		if t.Failed() {
			report.Outcome = "failed"
		}
		report.save(t)
		if file.Close() != nil {
			t.Error("acceptance report close failed")
		}
	})
	if !report.save(t) {
		t.Fatal("acceptance report must be writable before real requests")
	}

	t.Run("pipeline", func(t *testing.T) {
		c := acceptanceCase{Name: "pipeline", Input: acceptanceSpeech, Expected: "insert; preserve 语音工作室 / 声音 / 语速; fake commit exactly once", Review: []string{"Confirm ASR fidelity and that generated text remains the supplied greeting, without invented details.", "Recorder and injector are fake; no Windows acceptance is implied."}}
		before := ledger.snapshot()
		defer report.finish(t, &c, before, time.Now())
		cfg, keys := acceptanceLLMConfig(t, report, &c)
		values := acceptanceRequire(t, &c, "ASR_ENDPOINT", "ASR_MODEL", "ASR_KEY", "WAV")
		cfg.ASR.Endpoint, cfg.ASR.Model = values["ASR_ENDPOINT"], values["ASR_MODEL"]
		cfg.ASR.Provider = os.Getenv(acceptancePrefix + "ASR_PROVIDER")
		cfg.ASR.Language = "zh"
		keys["asr"] = values["ASR_KEY"]
		report.protect(values["ASR_KEY"], cfg.ASR.Endpoint)
		if cfg.Validate() != nil || settings.ValidKey(keys["asr"]) != nil {
			t.Fatal("invalid pipeline acceptance configuration")
		}
		data, err := acceptanceReadWAV(values["WAV"])
		if err != nil {
			t.Fatal("fixture must be valid non-quiet PCM16/16k/mono WAV within size limits")
		}
		asrClient := acceptanceClient(t, "asr", ledger)
		defer asrClient.CloseIdleConnections()
		processor := &acceptancePipeline{}
		injector := &fakeInjector{}
		controller := app.NewWithFactory(func(ctx context.Context, id string, intent app.Intent, notify func(app.Stage, float64)) (app.Processor, app.TaskOptions, error) {
			p, options, err := New(ctx, id, intent, cfg, keys, notify)
			if err != nil {
				processor.recordError(err)
				return nil, options, err
			}
			session := p.(*Session)
			session.Recorder = acceptanceRecorder{data: data}
			a, err := asr.New(asrClient, cfg.ASR, keys["asr"])
			if err != nil {
				processor.recordError(err)
				return nil, options, err
			}
			session.ASR = a
			l := session.LLM.(llm.Compatible)
			l.Client = acceptanceCountClient(l.Client, "llm", ledger)
			session.LLM = l
			processor.Session = session
			return processor, options, nil
		}, injector, time.Hour)
		defer controller.Close()
		controller.Toggle(app.Dictation)
		controller.Toggle(app.Dictation)
		timer := time.NewTimer(100 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			state := controller.Snapshot()
			if state.Stage == app.Done || state.Stage == app.Error || state.Stage == app.Skip || state.Stage == app.Clarify || state.Stage == app.Cancelled {
				c.Transcript = state.RawText
				c.Output = acceptanceOutput{Status: string(state.Stage), Text: state.Text}
				if state.Stage == app.Done {
					c.Output.Status = "insert"
				}
				c.Stage = string(state.Stage)
				c.ErrorClass, _ = processor.errorClass.Load().(string)
				// Notice on an error can be a native or configuration error, so never report it.
				acceptanceCheck(t, &c, "pipeline_done", state.Stage == app.Done)
				acceptanceCheck(t, &c, "asr_key_information", acceptanceAll(state.RawText, "语音工作室", "声音", "语速"))
				acceptanceCheck(t, &c, "body_key_information", acceptanceAll(state.Text, "语音工作室", "声音", "语速"))
				acceptanceCheck(t, &c, "fake_commit_once", injector.count.Load() == 1)
				delta, _ := ledger.delta(before)
				acceptanceCheck(t, &c, "one_asr_one_llm_zero_search", delta["asr"] == 1 && delta["llm"] == 1 && delta["exa"] == 0)
				return
			}
			select {
			case <-ticker.C:
			case <-timer.C:
				t.Fatal("pipeline exceeded bounded acceptance wait")
			}
		}
	})

	t.Run("semantics", func(t *testing.T) {
		cases := []struct {
			name, input, status string
			review              []string
			check               func(string) bool
		}{
			{"faithful", "嗯，那个，小岚，明天下午三点在图书馆门口见，记得带蓝色笔记本。", "insert", []string{"Check filler removal without changing time, meeting point, name, tone or notebook colour."}, func(s string) bool {
				return acceptanceAll(s, "小岚", "明天", "图书馆", "蓝色", "笔记本") && acceptanceAny(s, "三点", "3点", "三时", "3时", "15:00")
			}},
			{"romantic", "正文是：晚安，早点休息。请把这句话改得肉麻一点，发给我的虚构恋人小岚，不要添加约会或礼物承诺。", "insert", []string{"Assess affectionate rewriting and preserved good-night/rest intent; no invented promises or editing instructions in the body."}, func(s string) bool {
				return acceptanceAny(s, "晚安", "好梦", "睡", "休息") && !strings.Contains(s, "请把这句话")
			}},
			{"correction", "请整理：会议是周五下午三点，不对，改成周六下午四点，地点是图书馆。", "insert", []string{"Check that the latest correction replaces the original weekday/time, rather than discussing both."}, func(s string) bool {
				return acceptanceAny(s, "周六", "星期六", "礼拜六") && acceptanceAny(s, "四点", "4点", "四时", "4时", "16:00") && strings.Contains(s, "图书馆") && !acceptanceAny(s, "周五", "星期五", "礼拜五")
			}},
			{"target_ai", "帮我写一个 Python 函数，接收整数列表并返回其中的偶数，同时给出两个测试用例。", "insert", []string{"Verify that output is a request/prompt for another AI, not an executed solution. Code-marker checks are only a weak observable guard."}, func(s string) bool {
				return acceptanceAll(s, "Python", "偶数") && acceptanceAny(s, "函数", "列表") && !acceptanceAny(s, "```", "def ")
			}},
			{"translation", "把正文翻译成英文，只输出译文：明天上午九点开会，请带蓝色笔记本。", "insert", []string{"Review English translation, nine in the morning and absence of meta-instructions; keyword checks are not a language-quality evaluation."}, func(s string) bool {
				s = strings.ToLower(s)
				return acceptanceAll(s, "tomorrow", "blue", "notebook") && acceptanceAny(s, "nine", "9")
			}},
			{"skip", "只有会议改期才提醒小岚；会议没有改期，所以不用发消息。", "skip", []string{"Confirm no insertion is requested."}, func(s string) bool { return s == "" }},
			{"clarify", "查一下那里的天气。", "clarify", []string{"Confirm the notice asks for the missing city/location and contains no fabricated forecast."}, func(s string) bool { return s == "" }},
		}
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				c := acceptanceCase{Name: "semantics/" + item.name, Input: item.input, Expected: item.status, Review: item.review}
				before := ledger.snapshot()
				defer report.finish(t, &c, before, time.Now())
				cfg, keys := acceptanceLLMConfig(t, report, &c)
				client := acceptanceClient(t, "llm", ledger)
				defer client.CloseIdleConnections()
				// Advertise the real tool contract, but reject accidental ordinary searches
				// BEFORE any paid Exa request; an attempt is an assertion failure, not a retry.
				guard := &acceptanceSearch{}
				provider := llm.Compatible{Client: client, Config: cfg.LLM, Key: keys["llm"], Search: guard, MaxSearchCalls: 1}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				request := acceptanceRequest(item.input)
				if item.name == "faithful" || item.name == "correction" {
					request.Intent = app.Dictation
				}
				if item.name == "target_ai" {
					request.TargetApp = "ChatGPT.exe"
					request.Intent = app.Dictation
				}
				result, err := provider.Complete(ctx, request)
				c.capture(result, err)
				acceptanceCheck(t, &c, "valid_response", err == nil)
				acceptanceCheck(t, &c, "expected_status", err == nil && result.Status == item.status)
				acceptanceCheck(t, &c, "observable_content_constraints", err == nil && item.check(result.Text))
				acceptanceCheck(t, &c, "zero_search_attempts", guard.calls.Load() == 0)
				if item.name == "clarify" {
					asksLocation := acceptanceAny(result.Notice, "城市", "哪里", "地点", "地区", "位置") ||
						(strings.Contains(result.Notice, "那里") && acceptanceAny(result.Notice, "指", "具体", "哪个地方", "什么地方"))
					acceptanceCheck(t, &c, "asks_for_location", asksLocation)
				}
				delta, _ := ledger.delta(before)
				acceptanceCheck(t, &c, "one_llm_zero_exa", delta["llm"] == 1 && delta["exa"] == 0)
			})
		}
	})

	t.Run("exa", func(t *testing.T) {
		c := acceptanceCase{Name: "exa", Input: "site:go.dev/doc/faq Go programming language open source", Expected: "nonempty bounded evidence with an official go.dev URL", Review: []string{"Check retrieved official URLs support the stable fact; URL existence alone does not prove factual entailment."}}
		before := ledger.snapshot()
		defer report.finish(t, &c, before, time.Now())
		key := acceptanceRequire(t, &c, "EXA_KEY")["EXA_KEY"]
		report.protect(key)
		if settings.ValidKey(key) != nil {
			t.Fatal("invalid Exa acceptance key")
		}
		client := acceptanceClient(t, "exa", ledger)
		defer client.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		hits, err := (search.Exa{Client: client, Key: key, Config: settings.Default().Exa}).Search(ctx, c.Input)
		c.ErrorClass = acceptanceErrorClass(err)
		c.SourceURLs = acceptanceURLs(hits)
		acceptanceCheck(t, &c, "valid_response", err == nil)
		acceptanceCheck(t, &c, "bounded_nonempty_evidence", len(hits) > 0 && len(hits) <= 3)
		acceptanceCheck(t, &c, "official_source_url", acceptanceOfficial(c.SourceURLs))
		delta, _ := ledger.delta(before)
		acceptanceCheck(t, &c, "one_exa", delta["exa"] == 1)
	})

	t.Run("tool_loop", func(t *testing.T) {
		c := acceptanceCase{Name: "tool_loop", Input: "请实际检索 Go 官方网站 go.dev 的 FAQ，核实 Go 是否是开源编程语言，并把答案整理成一句简短中文正文。请只检索这一个稳定事实，不要仅凭记忆作答。", Expected: "insert; exactly one real search, two LLM calls, official source URL", Review: []string{"Check that final answer addresses the Go open-source fact and is supported by the returned official URL, not merely plausible wording."}}
		before := ledger.snapshot()
		defer report.finish(t, &c, before, time.Now())
		cfg, keys := acceptanceLLMConfig(t, report, &c)
		key := acceptanceRequire(t, &c, "EXA_KEY")["EXA_KEY"]
		report.protect(key)
		if settings.ValidKey(key) != nil {
			t.Fatal("invalid Exa acceptance key")
		}
		lc := acceptanceClient(t, "llm", ledger)
		defer lc.CloseIdleConnections()
		ec := acceptanceClient(t, "exa", ledger)
		defer ec.CloseIdleConnections()
		cfg.Exa.MaxCalls = 1
		guard := &acceptanceSearch{provider: search.Exa{Client: ec, Key: key, Config: cfg.Exa}, allowed: true}
		provider := llm.Compatible{Client: lc, Config: cfg.LLM, Key: keys["llm"], Search: guard, MaxSearchCalls: 1}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		result, err := provider.Complete(ctx, acceptanceRequest(c.Input))
		c.capture(result, err)
		// Keep already retrieved URLs even if the final LLM call fails.
		c.SourceURLs = acceptanceURLs(guard.hits)
		acceptanceCheck(t, &c, "valid_final_structure", err == nil)
		acceptanceCheck(t, &c, "insert_body", err == nil && result.Status == "insert" && strings.TrimSpace(result.Text) != "")
		acceptanceCheck(t, &c, "exactly_one_search", guard.calls.Load() == 1)
		acceptanceCheck(t, &c, "official_source_url", acceptanceOfficial(c.SourceURLs))
		acceptanceCheck(t, &c, "observable_topic", acceptanceAny(result.Text, "Go", "go") && acceptanceAny(result.Text, "开源", "open source", "open-source"))
		delta, _ := ledger.delta(before)
		acceptanceCheck(t, &c, "two_llm_one_exa", delta["llm"] == 2 && delta["exa"] == 1)
	})
}

type acceptanceOutput struct {
	Status string `json:"status"`
	Text   string `json:"text"`
	Notice string `json:"notice,omitempty"`
}
type acceptanceAssertion struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}
type acceptanceCase struct {
	Name       string                `json:"name"`
	Outcome    string                `json:"outcome"`
	Input      string                `json:"synthetic_input"`
	Expected   string                `json:"expected"`
	Transcript string                `json:"synthetic_transcript,omitempty"`
	Output     acceptanceOutput      `json:"output"`
	Stage      string                `json:"pipeline_stage,omitempty"`
	ErrorClass string                `json:"error_class,omitempty"`
	Checks     []acceptanceAssertion `json:"automatic_assertions"`
	Review     []string              `json:"main_thread_review_required"`
	SourceURLs []string              `json:"source_urls,omitempty"`
	Calls      map[string]int        `json:"http_calls"`
	ServiceMS  map[string]int64      `json:"service_elapsed_ms"`
	ElapsedMS  int64                 `json:"elapsed_ms"`
}
type acceptanceReport struct {
	Version        int              `json:"version"`
	StartedAt      time.Time        `json:"started_at"`
	FinishedAt     time.Time        `json:"finished_at"`
	Outcome        string           `json:"outcome"`
	Scope          string           `json:"scope"`
	Windows        string           `json:"windows_acceptance"`
	Pipeline       string           `json:"pipeline_acceptance"`
	SemanticReview string           `json:"semantic_review"`
	Budget         map[string]int   `json:"http_call_budget"`
	Calls          map[string]int   `json:"http_calls"`
	ServiceMS      map[string]int64 `json:"service_elapsed_ms"`
	Cases          []acceptanceCase `json:"cases"`
	file           *os.File
	ledger         *acceptanceLedger
	protected      []string
}

func (r *acceptanceReport) protect(values ...string) {
	for _, value := range values {
		if value != "" {
			r.protected = append(r.protected, value)
		}
	}
}
func (r *acceptanceReport) clean(value string) string {
	for _, secret := range r.protected {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}
func (r *acceptanceReport) finish(t *testing.T, c *acceptanceCase, before acceptanceSnapshot, started time.Time) {
	c.Outcome = "passed_automatic_checks_only"
	if t.Skipped() {
		c.Outcome = "skipped"
		if c.ErrorClass == "missing_configuration" {
			c.Outcome = "deferred_missing_configuration"
		}
	}
	if t.Failed() {
		c.Outcome = "failed"
	}
	c.ElapsedMS = time.Since(started).Milliseconds()
	c.Calls, c.ServiceMS = r.ledger.delta(before)
	c.Transcript = r.clean(c.Transcript)
	c.Output.Text, c.Output.Notice = r.clean(c.Output.Text), r.clean(c.Output.Notice)
	for i := range c.SourceURLs {
		c.SourceURLs[i] = r.clean(c.SourceURLs[i])
	}
	if c.Name == "pipeline" {
		r.Pipeline = c.Outcome
	}
	r.Cases = append(r.Cases, *c)
	r.save(t)
	t.Logf("%s: %s; asr=%d llm=%d exa=%d; %d ms", c.Name, c.Outcome, c.Calls["asr"], c.Calls["llm"], c.Calls["exa"], c.ElapsedMS)
}
func (r *acceptanceReport) save(t *testing.T) bool {
	snapshot := r.ledger.snapshot()
	r.Calls, r.ServiceMS = snapshot.calls, snapshot.elapsed
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Error("acceptance report encoding failed")
		return false
	}
	if _, err = r.file.Seek(0, io.SeekStart); err == nil {
		_, err = r.file.Write(append(body, '\n'))
	}
	if err == nil {
		err = r.file.Truncate(int64(len(body) + 1))
	}
	if err == nil {
		err = r.file.Sync()
	}
	if err != nil {
		t.Error("acceptance report persistence failed")
		return false
	}
	return true
}
func (c *acceptanceCase) capture(result llm.Result, err error) {
	c.Output = acceptanceOutput{Status: result.Status, Text: result.Text, Notice: result.Notice}
	c.SourceURLs = acceptanceURLs(result.Sources)
	c.ErrorClass = acceptanceErrorClass(err)
}
func acceptanceCheck(t *testing.T, c *acceptanceCase, name string, passed bool) {
	c.Checks = append(c.Checks, acceptanceAssertion{Name: name, Passed: passed})
	if !passed {
		t.Errorf("acceptance assertion failed: %s", name)
	}
}
func acceptanceRequire(t *testing.T, c *acceptanceCase, names ...string) map[string]string {
	values := map[string]string{}
	missing := []string{}
	for _, name := range names {
		values[name] = os.Getenv(acceptancePrefix + name)
		if values[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		c.ErrorClass = "missing_configuration"
		t.Skip("acceptance configuration missing: " + strings.Join(missing, ", "))
	}
	return values
}
func acceptanceLLMConfig(t *testing.T, r *acceptanceReport, c *acceptanceCase) (settings.Config, keyVault) {
	values := acceptanceRequire(t, c, "LLM_BASE_URL", "LLM_MODEL", "LLM_KEY")
	cfg := settings.Default()
	cfg.Mode = "live"
	cfg.LLM.BaseURL, cfg.LLM.Model = values["LLM_BASE_URL"], values["LLM_MODEL"]
	if format := os.Getenv(acceptancePrefix + "RESPONSE_FORMAT"); format != "" {
		cfg.LLM.OutputFormat = format
	}
	// Fixed fictional identity only; intentionally no city for the clarify case.
	cfg.Profile = settings.Profile{Name: "虚构测试人物小岚", Timezone: "Asia/Shanghai", CustomBackground: "所有人物与对话均为合成验收素材，不代表真实个人。"}
	r.protect(values["LLM_KEY"], cfg.LLM.BaseURL)
	if netapi.Endpoint(cfg.LLM.BaseURL) != nil || strings.TrimSpace(cfg.LLM.Model) == "" || len(cfg.LLM.Model) > 200 || strings.ContainsAny(cfg.LLM.Model, "\r\n") || settings.ValidKey(values["LLM_KEY"]) != nil || (cfg.LLM.OutputFormat != "json_schema" && cfg.LLM.OutputFormat != "json_object") {
		t.Fatal("invalid LLM acceptance configuration")
	}
	return cfg, keyVault{"llm": values["LLM_KEY"]}
}
func acceptanceRequest(input string) llm.Request {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		zone = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return llm.Request{Intent: app.Dialogue, TaskID: "synthetic-acceptance", ASRText: input, TargetApp: "synthetic-editor.exe", NowLocal: time.Now().In(zone), Background: "姓名：虚构测试人物小岚\n时区：Asia/Shanghai\n所有人物与对话均为合成验收素材，不代表真实个人。"}
}
func acceptanceAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
func acceptanceAny(s string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}
func acceptanceURLs(hits []search.Hit) []string {
	urls := []string{}
	for _, hit := range hits {
		u, err := url.Parse(hit.URL)
		if err == nil && u.User == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") {
			u.RawQuery = ""
			u.Fragment = ""
			urls = append(urls, u.String())
		}
	}
	return urls
}
func acceptanceOfficial(urls []string) bool {
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err == nil && u.Scheme == "https" && u.Hostname() == "go.dev" {
			return true
		}
	}
	return false
}
func acceptanceErrorClass(err error) string {
	if err == nil {
		return ""
	}
	var e *netapi.Error
	if errors.As(err, &e) && acceptanceAny("|auth|rate|server|network|timeout|cancelled|response|large|request|config|quiet|device|", "|"+e.Code+"|") && (e.Service == "asr" || e.Service == "llm" || e.Service == "exa") {
		return e.Service + ":" + e.Code
	}
	return "request_failed"
}

// Observe the real session's errors without retaining vendor bodies or config.
// Embedding Session preserves Begin and the coordinator's normal lifecycle.
type acceptancePipeline struct {
	*Session
	errorClass atomic.Value
}

func (p *acceptancePipeline) recordError(err error) {
	if err != nil {
		p.errorClass.Store(acceptanceErrorClass(err))
	}
}
func (p *acceptancePipeline) Transcribe(ctx context.Context) (string, error) {
	text, err := p.Session.Transcribe(ctx)
	p.recordError(err)
	return text, err
}
func (p *acceptancePipeline) Complete(ctx context.Context, raw string) (app.Result, error) {
	result, err := p.Session.Complete(ctx, raw)
	p.recordError(err)
	return result, err
}

type acceptanceRecorder struct{ data audio.Data }

func (r acceptanceRecorder) Start(context.Context, string, int, func(float64)) error { return nil }
func (r acceptanceRecorder) Stop(ctx context.Context) (audio.Data, error) {
	if ctx.Err() != nil {
		return audio.Data{}, ctx.Err()
	}
	return r.data, nil
}
func acceptanceReadWAV(path string) (audio.Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return audio.Data{}, audio.ErrQuiet
	}
	defer f.Close()
	wav, err := io.ReadAll(io.LimitReader(f, audio.MaxWAVBytes+1))
	if err != nil || len(wav) < 44 || len(wav) > audio.MaxWAVBytes || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return audio.Data{}, audio.ErrQuiet
	}
	var pcm []byte
	formatOK := false
	// ffmpeg can emit LIST or other chunks before data; do not assume a 44-byte header.
	for pos := 12; pos+8 <= len(wav); {
		n := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))
		start := pos + 8
		if n > len(wav)-start {
			return audio.Data{}, audio.ErrQuiet
		}
		chunk := wav[start : start+n]
		switch string(wav[pos : pos+4]) {
		case "fmt ":
			formatOK = n >= 16 && binary.LittleEndian.Uint16(chunk[0:2]) == 1 && binary.LittleEndian.Uint16(chunk[2:4]) == 1 && binary.LittleEndian.Uint32(chunk[4:8]) == 16000 && binary.LittleEndian.Uint16(chunk[12:14]) == 2 && binary.LittleEndian.Uint16(chunk[14:16]) == 16
		case "data":
			pcm = chunk
		}
		pos = start + n + n%2
	}
	if !formatOK || len(pcm) == 0 {
		return audio.Data{}, audio.ErrQuiet
	}
	samples, err := audio.Decode(pcm, audio.Format{Rate: 16000, Channels: 1, Bits: 16})
	if err != nil {
		return audio.Data{}, audio.ErrQuiet
	}
	data, err := audio.Encode(samples)
	if err != nil {
		return audio.Data{}, err
	}
	data.WAV = wav
	return data, nil
}

type acceptanceSearch struct {
	provider search.Provider
	allowed  bool
	calls    atomic.Int32
	hits     []search.Hit
}

func (s *acceptanceSearch) Search(ctx context.Context, query string) ([]search.Hit, error) {
	if s.calls.Add(1) > 1 || !s.allowed || s.provider == nil {
		return nil, netapi.Fail("exa", "request")
	}
	hits, err := s.provider.Search(ctx, query)
	s.hits = append(s.hits, hits...)
	return hits, err
}

type acceptanceSnapshot struct {
	calls   map[string]int
	elapsed map[string]int64
}
type acceptanceLedger struct {
	mu      sync.Mutex
	calls   map[string]int
	elapsed map[string]int64
}

func (l *acceptanceLedger) snapshot() acceptanceSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := acceptanceSnapshot{calls: map[string]int{}, elapsed: map[string]int64{}}
	for _, service := range []string{"asr", "llm", "exa"} {
		s.calls[service] = l.calls[service]
		s.elapsed[service] = l.elapsed[service]
	}
	return s
}
func (l *acceptanceLedger) delta(before acceptanceSnapshot) (map[string]int, map[string]int64) {
	after := l.snapshot()
	for service := range after.calls {
		after.calls[service] -= before.calls[service]
		after.elapsed[service] -= before.elapsed[service]
	}
	return after.calls, after.elapsed
}
func acceptanceClient(t *testing.T, service string, ledger *acceptanceLedger) *http.Client {
	client, err := netapi.Client()
	if err != nil {
		t.Fatal("acceptance proxy configuration invalid")
	}
	return acceptanceCountClient(client, service, ledger)
}
func acceptanceCountClient(client *http.Client, service string, ledger *acceptanceLedger) *http.Client {
	copy := *client
	copy.Transport = &acceptanceTransport{base: client.Transport, service: service, ledger: ledger}
	return &copy
}

type acceptanceTransport struct {
	base    http.RoundTripper
	service string
	ledger  *acceptanceLedger
}

func (tr *acceptanceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	limit := map[string]int{"asr": 1, "llm": 10, "exa": 2}[tr.service]
	tr.ledger.mu.Lock()
	if tr.ledger.calls[tr.service] >= limit {
		tr.ledger.mu.Unlock()
		return nil, netapi.Fail(tr.service, "request")
	}
	tr.ledger.calls[tr.service]++
	tr.ledger.mu.Unlock()
	started := time.Now()
	record := func() {
		tr.ledger.mu.Lock()
		tr.ledger.elapsed[tr.service] += time.Since(started).Milliseconds()
		tr.ledger.mu.Unlock()
	}
	res, err := tr.base.RoundTrip(req)
	if err != nil {
		record()
		return nil, err
	}
	res.Body = &acceptanceBody{ReadCloser: res.Body, finish: record}
	return res, nil
}
func (tr *acceptanceTransport) CloseIdleConnections() {
	if c, ok := tr.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type acceptanceBody struct {
	io.ReadCloser
	once   sync.Once
	finish func()
}

func (b *acceptanceBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.finish); return err }

// Offline evidence for the spending guard and private/partial report persistence.
// No external endpoint, runtime configuration or audio fixture is used here.
func TestAcceptanceHarnessOffline(t *testing.T) {
	ledger := &acceptanceLedger{calls: map[string]int{}, elapsed: map[string]int64{}}
	var forwarded int
	transport := &acceptanceTransport{service: "asr", ledger: ledger, base: acceptanceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		forwarded++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	req, _ := http.NewRequest("POST", "http://localhost/synthetic", nil)
	res, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal("offline transport failed")
	}
	res.Body.Close()
	res.Body.Close()
	if _, err = transport.RoundTrip(req); err == nil || forwarded != 1 {
		t.Fatal("budget failed to block excess request")
	}
	guard := &acceptanceSearch{}
	if _, err = guard.Search(context.Background(), "synthetic"); err == nil || guard.calls.Load() != 1 {
		t.Fatal("ordinary search guard did not reject")
	}
	file, err := os.OpenFile(t.TempDir()+"/report.json", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal("offline report setup failed")
	}
	defer file.Close()
	report := &acceptanceReport{file: file, ledger: ledger, Cases: []acceptanceCase{{Name: "partial", Outcome: "failed", ErrorClass: "llm:response"}}}
	report.save(t)
	report.Cases = append(report.Cases, acceptanceCase{Name: "later", Outcome: "skipped"})
	report.save(t)
	file.Seek(0, io.SeekStart)
	body, _ := io.ReadAll(file)
	var decoded acceptanceReport
	if json.Unmarshal(body, &decoded) != nil || len(decoded.Cases) != 2 || decoded.Cases[0].Outcome != "failed" {
		t.Fatal("partial report evidence was lost")
	}
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report must have mode 0600")
	}
	report.protect("synthetic-secret")
	if strings.Contains(report.clean("echo synthetic-secret"), "synthetic-secret") {
		t.Fatal("report redaction failed")
	}
	if acceptanceErrorClass(errors.New("raw-vendor-body")) != "request_failed" {
		t.Fatal("raw error classification leaked")
	}
	processor := &acceptancePipeline{}
	processor.recordError(nil)
	if processor.errorClass.Load() != nil {
		t.Fatal("successful pipeline recorded an error")
	}
	processor.recordError(netapi.Fail("asr", "response"))
	if processor.errorClass.Load() != "asr:response" {
		t.Fatal("pipeline lost safe service error classification")
	}
	processor.recordError(errors.New("raw-vendor-body synthetic-secret"))
	if processor.errorClass.Load() != "request_failed" {
		t.Fatal("pipeline raw error classification leaked")
	}
	if !bytes.Contains(body, []byte(`"asr": 1`)) {
		t.Fatal("report missing call count")
	}
}

type acceptanceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f acceptanceRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
