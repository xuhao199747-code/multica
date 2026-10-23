package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Release struct {
	Tag, CommitSHA, URL string
}

type ReleaseSource interface {
	LatestStable(context.Context) (Release, error)
}

// GitHubReleaseSource resolves releases only from the official repository.
// Its zero value uses the public GitHub API with a bounded request timeout.
type GitHubReleaseSource struct {
	BaseURL string
}

var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func (s GitHubReleaseSource) LatestStable(ctx context.Context) (Release, error) {
	baseURL := strings.TrimRight(s.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := &http.Client{Timeout: 15 * time.Second}
	var release struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := githubJSON(ctx, client, baseURL+"/repos/multica-ai/multica/releases/latest", &release); err != nil {
		return Release{}, fmt.Errorf("fetch latest release: %w", err)
	}
	if release.Draft || release.Prerelease || strings.TrimSpace(release.Tag) == "" {
		return Release{}, fmt.Errorf("latest release must be published, stable, and tagged")
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := githubJSON(ctx, client, baseURL+"/repos/multica-ai/multica/commits/"+url.PathEscape(release.Tag), &commit); err != nil {
		return Release{}, fmt.Errorf("resolve release commit: %w", err)
	}
	if !commitSHA.MatchString(commit.SHA) {
		return Release{}, fmt.Errorf("release commit must have a 40-character hexadecimal SHA")
	}
	return Release{Tag: release.Tag, CommitSHA: commit.SHA, URL: release.URL}, nil
}

func githubJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}
