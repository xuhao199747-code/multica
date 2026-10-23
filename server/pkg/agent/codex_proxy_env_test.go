package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCodexProxyEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"new agent", []string{"PATH=/usr/bin", "HTTPS_PROXY=http://proxy:7892"}},
		{"existing exclusions", []string{"NO_PROXY=internal.example", "no_proxy=other.example", "HTTP_PROXY=http://proxy:7892"}},
		{"already configured", []string{"NO_PROXY=localhost,127.0.0.1,::1", "no_proxy=*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := codexProxyEnv(tc.env)
			values := map[string]string{}
			for _, entry := range got {
				k, v, _ := strings.Cut(entry, "=")
				if _, exists := values[k]; exists {
					t.Fatalf("duplicate environment key %s", k)
				}
				values[k] = v
			}
			for _, key := range []string{"NO_PROXY", "no_proxy"} {
				for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
					if !strings.Contains(","+values[key]+",", ","+host+",") {
						t.Errorf("%s missing loopback host %s: %q", key, host, values[key])
					}
				}
			}
			for _, entry := range tc.env {
				k, v, _ := strings.Cut(entry, "=")
				if k == "NO_PROXY" || k == "no_proxy" {
					for _, host := range strings.Split(v, ",") {
						for _, key := range []string{"NO_PROXY", "no_proxy"} {
							if !strings.Contains(","+values[key]+",", ","+host+",") {
								t.Errorf("lost exclusion %s from %s", host, key)
							}
						}
					}
				} else if values[k] != v {
					t.Errorf("changed %s: %q", k, values[k])
				}
			}
		})
	}
}

func TestCodexLaunchAddsLoopbackBypassWithoutAgentConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}
	capture := filepath.Join(t.TempDir(), "env")
	fakePath := writeFakeCodexAppServer(t, "printf '%s\\n' \"$NO_PROXY\" \"$no_proxy\" \"$HTTPS_PROXY\" \"$OPENAI_BASE_URL\" > \"$PROXY_ENV_FILE\"\nexit 2\n")
	backend, err := New("codex", Config{ExecutablePath: fakePath, Env: map[string]string{
		"PROXY_ENV_FILE":  capture,
		"HTTPS_PROXY":     "http://remote-proxy:7892",
		"OPENAI_BASE_URL": "https://provider.example/v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.Execute(context.Background(), "prompt", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	<-session.Result
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || lines[2] != "http://remote-proxy:7892" || lines[3] != "https://provider.example/v1" {
		t.Fatalf("remote provider settings changed: %q", data)
	}
	for _, value := range lines[:2] {
		for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
			if !strings.Contains(","+value+",", ","+host+",") {
				t.Fatalf("launched Codex missing loopback exclusion %s: %q", host, value)
			}
		}
	}
}
