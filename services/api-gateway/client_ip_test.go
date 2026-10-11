package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// headerUpstream records the forwarding headers the services would receive.
func headerUpstream(t *testing.T) *http.Header {
	t.Helper()
	got := http.Header{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)
	routes = map[string]string{"/api/orders": up.URL}
	return &got
}

func TestForwardKeepsTheClientChainFromATrustedProxy(t *testing.T) {
	got := headerUpstream(t)
	setLimits(t, 100, 100, 100, "10.0.0.0/8")
	tok := tokenFor(t, "alice@test.com")

	r := request("GET", "/api/orders/1", "10.0.2.2:5555", "")
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	rec := httptest.NewRecorder()
	handleProxy(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if xff := got.Get("X-Forwarded-For"); xff != "198.51.100.9, 10.0.2.2" {
		t.Fatalf("services should see the client first and this hop last, got %q", xff)
	}
	if rip := got.Get("X-Real-IP"); rip != "198.51.100.9" {
		t.Fatalf("X-Real-IP should be the real client, got %q", rip)
	}
}

func TestForwardDropsForgedHeadersFromAnyoneElse(t *testing.T) {
	got := headerUpstream(t)
	setLimits(t, 100, 100, 100, "10.0.0.0/8")
	tok := tokenFor(t, "alice@test.com")

	r := request("GET", "/api/orders/1", "203.0.113.7:5555", "")
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "1.2.3.4")
	rec := httptest.NewRecorder()
	handleProxy(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if xff := got.Get("X-Forwarded-For"); xff != "203.0.113.7" {
		t.Fatalf("a client's own X-Forwarded-For must not reach the services, got %q", xff)
	}
	if rip := got.Get("X-Real-IP"); rip != "203.0.113.7" {
		t.Fatalf("X-Real-IP must be the real peer, got %q", rip)
	}
}

func TestBadTokensAreCountedPerClientIP(t *testing.T) {
	useTestRedis(t)
	fakeUpstream(t)
	setLimits(t, 100, 100, 3, "10.0.0.0/8")
	good := tokenFor(t, "alice@test.com")

	call := func(peer, forwarded, token string) int {
		r := request("GET", "/api/orders/1", peer, "")
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handleProxy(rec, r)
		return rec.Code
	}

	// Three bad tokens from one real client, arriving through different proxy pods.
	for i := 1; i <= 3; i++ {
		peer := fmt.Sprintf("10.0.2.%d:4000", i)
		if code := call(peer, "198.51.100.9", "not-a-token"); code != http.StatusUnauthorized {
			t.Fatalf("bad token %d: want 401, got %d", i, code)
		}
	}
	// The fourth is refused outright, with or without a token.
	if code := call("10.0.2.9:4000", "198.51.100.9", "not-a-token"); code != http.StatusTooManyRequests {
		t.Fatalf("fourth bad token: want 429, got %d", code)
	}
	if code := call("10.0.2.9:4000", "198.51.100.9", ""); code != http.StatusTooManyRequests {
		t.Fatalf("missing token after the limit: want 429, got %d", code)
	}
	// A valid token from the same IP still works: only failures are counted.
	if code := call("10.0.2.9:4000", "198.51.100.9", good); code != http.StatusOK {
		t.Fatalf("good token from the blocked IP: want 200, got %d", code)
	}
	// Another client behind the same proxies is not affected.
	if code := call("10.0.2.1:4000", "198.51.100.77", "not-a-token"); code != http.StatusUnauthorized {
		t.Fatalf("another IP: want 401, got %d", code)
	}
}
