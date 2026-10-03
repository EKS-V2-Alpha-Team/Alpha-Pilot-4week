package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// routingSetup points every route at one fake upstream that records the path it received.
func routingSetup(t *testing.T) *string {
	t.Helper()
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	jwtSecret = []byte("test-secret")
	routes = map[string]string{
		"/api/orders":    upstream.URL,
		"/api/dashboard": upstream.URL,
		"/dashboard/":    upstream.URL,
	}
	return &gotPath
}

func proxyRequest(t *testing.T, path string, withToken bool) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if withToken {
		tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "admin@platform.local", "exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString(jwtSecret)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	handleProxy(rec, req)
	return rec.Code
}

func TestDashboardPageIsServedWithoutToken(t *testing.T) {
	got := routingSetup(t)
	if code := proxyRequest(t, "/", false); code != http.StatusOK || *got != "/" {
		t.Errorf("GET /: code %d, upstream path %q; want 200 and /", code, *got)
	}
}

func TestDashboardDataKeepsItsPath(t *testing.T) {
	got := routingSetup(t)
	if code := proxyRequest(t, "/dashboard/summary", true); code != http.StatusOK || *got != "/dashboard/summary" {
		t.Errorf("GET /dashboard/summary: code %d, upstream path %q; want 200 and /dashboard/summary", code, *got)
	}
}

func TestDashboardDataNeedsToken(t *testing.T) {
	routingSetup(t)
	if code := proxyRequest(t, "/dashboard/summary", false); code != http.StatusUnauthorized {
		t.Errorf("GET /dashboard/summary without token: code %d, want 401", code)
	}
}

func TestOtherRoutesStillStripTheirPrefix(t *testing.T) {
	got := routingSetup(t)
	if code := proxyRequest(t, "/api/orders/42", true); code != http.StatusOK || *got != "/42" {
		t.Errorf("GET /api/orders/42: code %d, upstream path %q; want 200 and /42", code, *got)
	}
	if code := proxyRequest(t, "/api/dashboard/dashboard/summary", true); code != http.StatusOK || *got != "/dashboard/summary" {
		t.Errorf("old /api/dashboard route: code %d, upstream path %q; want 200 and /dashboard/summary", code, *got)
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	routingSetup(t)
	if code := proxyRequest(t, "/nope", true); code != http.StatusNotFound {
		t.Errorf("GET /nope: code %d, want 404", code)
	}
}
