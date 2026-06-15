package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	const tok = "s3cret-token-value"
	h := authMiddleware(next, tok)

	cases := []struct {
		name  string
		setup func(*http.Request)
		want  int
	}{
		{"valid bearer header", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }, http.StatusTeapot},
		{"missing credentials", func(*http.Request) {}, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"query param is rejected", func(r *http.Request) { r.URL.RawQuery = "token=" + tok }, http.StatusUnauthorized},
		{"missing bearer prefix", func(r *http.Request) { r.Header.Set("Authorization", tok) }, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			c.setup(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
			if c.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 response must carry a WWW-Authenticate header")
			}
		})
	}

	// An empty token disables authentication (loopback/dev use).
	rec := httptest.NewRecorder()
	authMiddleware(next, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("empty token should allow request, got %d", rec.Code)
	}
}
