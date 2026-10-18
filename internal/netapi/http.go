package netapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"voice_input/internal/i18n"
)

type Error struct {
	Service string
	Code    string
	Status  int
}

func (e *Error) I18nMessage() i18n.Message {
	service := e.Service
	switch service {
	case "asr", "llm", "exa", "audio":
	default:
		service = "service"
	}
	switch e.Code {
	case "mode", "dictation_clarify", "auth", "rate", "server", "network", "timeout", "cancelled", "response", "large", "upload_large", "request", "config", "quiet", "device":
		return i18n.M("net." + service + "." + e.Code)
	default:
		return i18n.Message{}
	}
}

func (e *Error) Error() string {
	label := map[string]string{"asr": "语音识别", "llm": "语义整理", "exa": "Exa 搜索", "audio": "录音"}[e.Service]
	if label == "" {
		label = "服务"
	}
	messages := map[string]string{"mode": "AI 对话需要真实 ASR + LLM 模式；请在设置中切换工作模式", "dictation_clarify": "润色模式不追问或执行任务；请复制原文，或用 AI 对话快捷键重新开始", "auth": "密钥无效或无权限", "rate": "请求过于频繁或额度不足", "server": "服务暂时不可用", "network": "网络连接失败，请检查地址与代理", "timeout": "请求超时", "cancelled": "已取消", "response": "响应格式无效", "large": "响应超过大小限制", "upload_large": "上传音频过大，请缩短录音后重试", "request": "请求不被服务支持，请检查模型与协议设置", "config": "请先配置服务地址、模型及密钥", "quiet": "录音过短或接近静音；请检查麦克风后重试", "device": "麦克风不可用；检查设备、权限和独占占用"}
	return label + "：" + messages[e.Code]
}
func Fail(service, code string) error { return &Error{Service: service, Code: code} }
func SafeMessage(err error, fallback string) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Error()
	}
	return fallback
}
func Endpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return i18n.Error("net.endpoint")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && Loopback(u.Hostname())) {
		return i18n.Error("net.https")
	}
	return nil
}
func Loopback(host string) bool {
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
func Client() (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if raw := os.Getenv("PROXY_URL"); raw != "" {
		proxy, err := url.Parse(raw)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return nil, i18n.Error("net.proxy")
		}
		transport.Proxy = func(r *http.Request) (*url.URL, error) {
			if Loopback(r.URL.Hostname()) {
				return nil, nil
			}
			return proxy, nil
		}
	} else {
		transport.Proxy = func(r *http.Request) (*url.URL, error) {
			if Loopback(r.URL.Hostname()) {
				return nil, nil
			}
			return http.ProxyFromEnvironment(r)
		}
	}
	transport.ResponseHeaderTimeout = 0 // per-provider contexts own the configured timeout
	return &http.Client{Transport: transport, Timeout: 0, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func Do(ctx context.Context, client *http.Client, req *http.Request, service string, limit int64) ([]byte, error) {
	if client == nil {
		return nil, Fail(service, "config")
	}
	res, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, transportError(ctx, service)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		code := "request"
		switch {
		case res.StatusCode == 401 || res.StatusCode == 403:
			code = "auth"
		case res.StatusCode == 429:
			code = "rate"
		case res.StatusCode >= 500:
			code = "server"
		}
		// Never expose vendor response bodies, endpoint URLs, or request headers.
		return nil, &Error{Service: service, Code: code, Status: res.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, transportError(ctx, service)
	}
	if int64(len(data)) > limit {
		return nil, Fail(service, "large")
	}
	return data, nil
}
func CheckJSON(data []byte) error {
	if !utf8.Valid(data) {
		return i18n.Error("json.encoding")
	}
	scanner := json.NewDecoder(bytes.NewReader(data))
	if err := scanValue(scanner, 0); err != nil {
		return err
	}
	if _, err := scanner.Token(); err != io.EOF {
		return i18n.Error("json.trailing")
	}

	return nil
}
func StrictJSON(data []byte, out any) error {
	if err := CheckJSON(data); err != nil {
		return err
	}

	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return i18n.Error("json.fields")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return i18n.Error("json.trailing")
	}
	return nil
}
func Timeout(seconds int) time.Duration { return time.Duration(seconds) * time.Second }
func ModelPath(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

// Reject duplicate keys and excessive nesting instead of silently accepting the
// last occurrence of a security-relevant field such as status or tool name.
func scanValue(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return i18n.Error("json.depth")
	}
	token, err := dec.Token()
	if err != nil {
		return i18n.Error("json.invalid")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return i18n.Error("json.invalid")
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return i18n.Error("json.duplicate")
			}
			keys[name] = true
			if err := scanValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := scanValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return i18n.Error("json.invalid")
	}
	closing, err := dec.Token()
	if err != nil || delimiter == '{' && closing != json.Delim('}') || delimiter == '[' && closing != json.Delim(']') {
		return i18n.Error("json.invalid")
	}
	return nil
}

func transportError(ctx context.Context, service string) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return Fail(service, "cancelled")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Fail(service, "timeout")
	}
	return Fail(service, "network")
}
