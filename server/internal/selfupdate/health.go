package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func VerifyDeployment(ctx context.Context, client *http.Client, e DeployEvidence) error {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	// Redirects must not make a failing deployment look healthy at another URL.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if bounded.Timeout == 0 {
		bounded.Timeout = 15 * time.Second
	}
	for _, target := range []struct {
		url   string
		ready bool
	}{{e.ReadyURL, true}, {e.LocalURL, false}, {e.ExternalURL, false}} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
		if err != nil {
			return fmt.Errorf("invalid deployment health URL")
		}
		response, err := bounded.Do(req)
		if err != nil {
			return fmt.Errorf("deployment health request failed")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return fmt.Errorf("deployment health returned non-200")
		}
		if target.ready {
			var body struct {
				Checks struct {
					DB         string `json:"db"`
					Migrations string `json:"migrations"`
				} `json:"checks"`
			}
			dec := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
			err = dec.Decode(&body)
			var extra any
			if err != nil || dec.Decode(&extra) != io.EOF || body.Checks.DB != "ok" || body.Checks.Migrations != "ok" {
				response.Body.Close()
				return fmt.Errorf("deployment database and migrations are not ready")
			}
		}
		response.Body.Close()
	}
	return nil
}
