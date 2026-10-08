package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// allowlistSetup points every route at one fake upstream and counts how many requests reached it.
func allowlistSetup(t *testing.T) *int64 {
	t.Helper()
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	jwtSecret = []byte("test-secret")
	setLimits(t, 1000, 1000, 1000, "")
	routes = map[string]string{
		"/api/orders":        upstream.URL,
		"/api/inventory":     upstream.URL,
		"/api/payments":      upstream.URL,
		"/api/notifications": upstream.URL,
		"/api/shipping":      upstream.URL,
		"/api/dashboard":     upstream.URL,
		"/dashboard/":        upstream.URL,
	}
	return &hits
}

func gatewayRequest(method, path string, loggedIn bool) int {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "198.51.100.7:1234"
	if loggedIn {
		tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "admin@platform.local", "exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString(jwtSecret)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	handleProxy(rec, req)
	return rec.Code
}

// Everything the dashboard page and the health grid call must still get through.
func TestRoutesTheDashboardUsesAreForwarded(t *testing.T) {
	hits := allowlistSetup(t)
	calls := []struct{ method, path string }{
		{"GET", "/api/orders/"}, {"POST", "/api/orders/"}, {"PUT", "/api/orders/status"}, {"GET", "/api/orders/42"},
		{"GET", "/api/inventory/products"}, {"POST", "/api/inventory/products"}, {"GET", "/api/inventory/products/p1"},
		{"GET", "/api/inventory/low-stock"},
		{"GET", "/api/payments/ledger"}, {"GET", "/api/payments/balance/alice"},
		{"GET", "/api/shipping/shipments"}, {"GET", "/api/shipping/shipments/7"}, {"GET", "/api/shipping/track/RM123"},
		{"GET", "/api/notifications/history"}, {"GET", "/api/notifications/templates"},
		{"GET", "/dashboard/summary"}, {"GET", "/dashboard/revenue"}, {"GET", "/dashboard/shipping/overview"},
		{"GET", "/api/dashboard/dashboard/summary"},
		{"GET", "/api/orders/healthz"}, {"GET", "/api/inventory/healthz"}, {"GET", "/api/payments/healthz"},
		{"GET", "/api/notifications/healthz"}, {"GET", "/api/shipping/healthz"},
	}
	for _, c := range calls {
		if code := gatewayRequest(c.method, c.path, true); code != http.StatusOK {
			t.Errorf("%s %s: got %d, want 200", c.method, c.path, code)
		}
	}
	if got := atomic.LoadInt64(hits); int(got) != len(calls) {
		t.Errorf("upstream saw %d requests, want %d", got, len(calls))
	}
}

// Routes meant for other services answer 404 at the gateway and never reach the service,
// even for a logged-in user.
func TestInternalRoutesAreNotForwarded(t *testing.T) {
	hits := allowlistSetup(t)
	blocked := []struct{ method, path string }{
		{"POST", "/api/payments/charge"}, {"POST", "/api/payments/refund"},
		{"POST", "/api/inventory/reserve"}, {"POST", "/api/inventory/release"},
		{"POST", "/api/inventory/commit"}, {"POST", "/api/inventory/restock"},
		{"PUT", "/api/inventory/products/p1"}, {"DELETE", "/api/inventory/products/p1"},
		{"POST", "/api/notifications/send"},
		{"POST", "/api/shipping/shipments"}, {"PUT", "/api/shipping/shipments/by-order/42"},
		{"GET", "/api/shipping/shipments/by-order/42"},
		{"DELETE", "/api/orders/42"}, {"PUT", "/api/orders/42"}, {"POST", "/api/orders/status"},
		{"POST", "/api/dashboard/anything"}, {"POST", "/dashboard/summary"},
		{"GET", "/api/payments/"}, {"GET", "/api/orders/42/events"},
	}
	for _, c := range blocked {
		if code := gatewayRequest(c.method, c.path, true); code != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404", c.method, c.path, code)
		}
	}
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Errorf("%d blocked requests reached a service", got)
	}
}

// Odd spellings of an allowed route must not slip through to a blocked one.
func TestPathTricksAreNotForwarded(t *testing.T) {
	hits := allowlistSetup(t)
	for _, path := range []string{
		"/api/inventory/products/../restock",
		"/api/inventory/products/./../reserve",
		"/api/inventory//restock",
		"/api/inventoryrestock",
		"/api/ordersX/status",
		"/api/orders/42/extra",
		"/api/shipping/shipments/by-order/42/",
	} {
		if code := gatewayRequest("POST", path, true); code == http.StatusOK {
			t.Errorf("POST %s was forwarded", path)
		}
		if code := gatewayRequest("PUT", path, true); code == http.StatusOK {
			t.Errorf("PUT %s was forwarded", path)
		}
	}
	// "*" matches one real segment, never "." or "..", which a service could resolve to its parent.
	for _, path := range []string{"/api/inventory/products/..", "/api/orders/.", "/api/shipping/shipments/.."} {
		if code := gatewayRequest("GET", path, true); code == http.StatusOK {
			t.Errorf("GET %s was forwarded", path)
		}
	}
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Errorf("%d tricky requests reached a service", got)
	}
}

// The carrier webhook is public and must keep working without a token; the route next to it must not.
func TestWebhookStaysOpenButNothingElseUnderShipping(t *testing.T) {
	allowlistSetup(t)
	if code := gatewayRequest("POST", "/api/shipping/webhook", false); code != http.StatusOK {
		t.Errorf("webhook without token: got %d, want 200", code)
	}
	if code := gatewayRequest("GET", "/api/shipping/webhook", false); code != http.StatusNotFound {
		t.Errorf("GET webhook: got %d, want 404", code)
	}
	if code := gatewayRequest("POST", "/api/shipping/shipments", false); code != http.StatusUnauthorized {
		t.Errorf("creating a shipment without a token: got %d, want 401", code)
	}
}

func TestIsExposedMatching(t *testing.T) {
	cases := []struct {
		prefix, method, path string
		want                 bool
	}{
		{"/api/orders", "GET", "/", true},
		{"/api/orders", "GET", "/42", true},
		{"/api/orders", "GET", "/42/x", false},
		{"/api/orders", "PUT", "/status", true},
		{"/api/orders", "PUT", "/42", false},
		{"/api/payments", "POST", "/charge", false},
		{"/api/unknown", "GET", "/", false},
		{"/dashboard/", "GET", "/dashboard/summary", true},
		{"/api/orders", "GET", "status", false},
	}
	for _, c := range cases {
		if got := isExposed(c.prefix, c.method, c.path); got != c.want {
			t.Errorf("isExposed(%s, %s, %s) = %v, want %v", c.prefix, c.method, c.path, got, c.want)
		}
	}
}
