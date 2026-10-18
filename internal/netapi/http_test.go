package netapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpoint(t *testing.T) {
	for _, raw := range []string{"https://service.example/v1", "http://127.0.0.1:123/v1", "http://[::1]:123/v1", "http://localhost/v1"} {
		if Endpoint(raw) != nil {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"http://external.example/v1", "ftp://x", "https://key@x/v1", "https://x?key=secret", "https://x#key", ""} {
		if Endpoint(raw) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
func TestStrictJSON(t *testing.T) {
	for _, data := range []string{`{"field":"x","field":"y"}`, `{"other":"x"}`, `{"field":"x"} {}`, `{"field":null,}`, string([]byte{0xff})} {
		var out struct {
			Field string `json:"field"`
		}
		if StrictJSON([]byte(data), &out) == nil {
			t.Errorf("accepted %q", data)
		}
	}
	var out struct {
		Field string `json:"field"`
	}
	if StrictJSON([]byte(`{"field":"value"}`), &out) != nil || out.Field != "value" {
		t.Fatal("valid json rejected")
	}
}
func TestProxyAndRedaction(t *testing.T) {
	t.Setenv("PROXY_URL", "http://127.0.0.1:1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("secret-key response body"))
	}))
	defer server.Close()
	client, err := Client()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", server.URL, nil)
	_, err = Do(context.Background(), client, req, "llm", 100)
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "密钥") {
		t.Fatalf("bad error: %v", err)
	}
}
func TestResponseLimitAndRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/secret", 302)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer server.Close()
	client, _ := Client()
	for _, path := range []string{"/large", "/redirect"} {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		if _, err := Do(context.Background(), client, req, "asr", 10); err == nil {
			t.Fatal("unsafe response accepted")
		}
	}
}

func TestBodyReadTimeoutIsClassified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(60 * time.Millisecond)
	}))
	defer server.Close()
	client, _ := Client()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequest("GET", server.URL, nil)
	_, err := Do(ctx, client, request, "llm", 100)
	var apiError *Error
	if !errors.As(err, &apiError) || apiError.Code != "timeout" {
		t.Fatalf("body timeout misclassified: %v", err)
	}
}

func TestUploadAndResponseSizeErrorsAreDistinct(t *testing.T) {
	upload := Fail("asr", "upload_large")
	response := Fail("asr", "large")
	if !strings.Contains(SafeMessage(upload, "fallback"), "上传音频过大") || strings.Contains(upload.Error(), "响应") {
		t.Fatal("upload error must advise shortening audio, not describe response size")
	}
	if !strings.Contains(response.Error(), "响应超过大小限制") || strings.Contains(response.Error(), "上传") {
		t.Fatal("response size error changed")
	}
}
