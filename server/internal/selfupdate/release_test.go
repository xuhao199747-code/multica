package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestStable(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const releaseURL = "https://github.com/multica-ai/multica/releases/tag/v1.2.3"
	stable := fmt.Sprintf(`{"tag_name":"v1.2.3","html_url":%q,"draft":false,"prerelease":false}`, releaseURL)
	for _, tc := range []struct {
		name, release, commit       string
		releaseStatus, commitStatus int
		wantError                   bool
	}{
		{name: "stable", release: stable, commit: fmt.Sprintf(`{"sha":%q}`, sha)},
		{name: "prerelease", release: `{"tag_name":"v1.2.3","prerelease":true}`, wantError: true},
		{name: "draft", release: `{"tag_name":"v1.2.3","draft":true}`, wantError: true},
		{name: "missing tag", release: `{"html_url":"https://github.com/multica-ai/multica"}`, wantError: true},
		{name: "invalid SHA", release: stable, commit: `{"sha":"not-a-sha"}`, wantError: true},
		{name: "short SHA", release: stable, commit: `{"sha":"0123456"}`, wantError: true},
		{name: "missing SHA", release: stable, commit: `{}`, wantError: true},
		{name: "release rate limited", releaseStatus: 429, release: stable, wantError: true},
		{name: "commit rate limited", release: stable, commitStatus: 429, wantError: true},
		{name: "malformed release", release: `{`, wantError: true},
		{name: "malformed commit", release: stable, commit: `{`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				status, body := tc.releaseStatus, tc.release
				switch r.URL.Path {
				case "/repos/multica-ai/multica/releases/latest":
				case "/repos/multica-ai/multica/commits/v1.2.3":
					status, body = tc.commitStatus, tc.commit
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			source := GitHubReleaseSource{BaseURL: server.URL}
			got, err := source.LatestStable(context.Background())
			if tc.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				if got != (Release{}) {
					t.Fatalf("error returned a guessed release: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := (Release{Tag: "v1.2.3", CommitSHA: sha, URL: releaseURL}); got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}
