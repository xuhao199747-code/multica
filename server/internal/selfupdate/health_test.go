package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyDeployment(t *testing.T) {
	for _, mode := range []string{"success", "db missing", "migrations missing", "bad ready", "local failed", "external failed", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/readyz":
					switch mode {
					case "db missing":
						w.Write([]byte(`{"checks":{"migrations":"ok"}}`))
					case "migrations missing":
						w.Write([]byte(`{"checks":{"db":"ok"}}`))
					case "bad ready":
						w.WriteHeader(503)
					case "malformed":
						w.Write([]byte(`{`))
					default:
						w.Write([]byte(`{"checks":{"db":"ok","migrations":"ok"}}`))
					}
				case "/local":
					if mode == "local failed" {
						w.WriteHeader(503)
					}
				case "/external":
					if mode == "external failed" {
						w.WriteHeader(503)
					}
				}
			}))
			defer s.Close()
			err := VerifyDeployment(context.Background(), s.Client(), DeployEvidence{ReadyURL: s.URL + "/readyz", LocalURL: s.URL + "/local", ExternalURL: s.URL + "/external"})
			if (err != nil) != (mode != "success") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
