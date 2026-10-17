package main

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestSpecVersionAndCommentRoutes(t *testing.T) {
	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	for _, tc := range []struct{ method, path, version string }{
		{http.MethodGet, "/api/specs/spec/versions/3", "3"},
		{http.MethodGet, "/api/specs/spec/versions/00000000-0000-0000-0000-000000000001/comments", "00000000-0000-0000-0000-000000000001"},
		{http.MethodPost, "/api/specs/spec/versions/00000000-0000-0000-0000-000000000001/comments", "00000000-0000-0000-0000-000000000001"},
	} {
		ctx := chi.NewRouteContext()
		if !router.Match(ctx, tc.method, tc.path) {
			t.Errorf("route not found: %s %s", tc.method, tc.path)
			continue
		}
		if got := ctx.URLParam("number"); got != tc.version {
			t.Errorf("%s: version = %q, want %q", tc.path, got, tc.version)
		}
	}
}
