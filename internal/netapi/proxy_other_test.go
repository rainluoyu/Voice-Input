//go:build !windows

package netapi

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSystemProxyEnvironment(t *testing.T) {
	if os.Getenv("VOICE_INPUT_PROXY_ENV_CHILD") == "1" {
		client, err := Client(DefaultProxyConfig())
		if err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest(http.MethodGet, os.Getenv("VOICE_INPUT_PROXY_TARGET"), nil)
		proxy, err := client.Transport.(*http.Transport).Proxy(request)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if proxy != nil {
			got = proxy.String()
		}
		if want := os.Getenv("VOICE_INPUT_PROXY_WANT"); got != want {
			t.Fatalf("got proxy %q, want %q", got, want)
		}
		return
	}
	for _, tt := range []struct {
		name, target, want string
		env                map[string]string
	}{
		{"http_upper", "http://service.example", "http://http-proxy.example:8080", map[string]string{"HTTP_PROXY": "http://http-proxy.example:8080"}},
		{"https_upper", "https://service.example", "http://https-target-proxy.example:8080", map[string]string{"HTTPS_PROXY": "http://https-target-proxy.example:8080"}},
		{"lowercase", "https://service.example", "socks5://proxy.example:1080", map[string]string{"https_proxy": "socks5://proxy.example:1080"}},
		{"no_proxy", "https://service.internal.example", "", map[string]string{"HTTPS_PROXY": "http://proxy.example:8080", "NO_PROXY": ".internal.example"}},
		{"lowercase_bypass", "https://service.internal.example", "", map[string]string{"https_proxy": "http://proxy.example:8080", "no_proxy": ".internal.example"}},
		{"uppercase_precedence", "https://service.example", "http://upper.example:8080", map[string]string{"HTTPS_PROXY": "http://upper.example:8080", "https_proxy": "http://lower.example:8080"}},
		{"legacy_ignored", "https://service.example", "", map[string]string{"PROXY_URL": "unsupported://invalid"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// ProxyFromEnvironment caches its first environment process-wide.
			// Each case therefore executes in a clean child, not behind t.Setenv.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSystemProxyEnvironment$")
			for _, entry := range os.Environ() {
				key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
				switch key {
				case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "PROXY_URL", "REQUEST_METHOD":
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, "VOICE_INPUT_PROXY_ENV_CHILD=1", "VOICE_INPUT_PROXY_TARGET="+tt.target, "VOICE_INPUT_PROXY_WANT="+tt.want)
			for key, value := range tt.env {
				command.Env = append(command.Env, key+"="+value)
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("proxy environment child failed: %v\n%s", err, output)
			}
		})
	}
}
