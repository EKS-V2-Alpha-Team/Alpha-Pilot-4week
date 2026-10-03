package main

// Rate limiter tests, one group per requirement (R1–R7).
//
// Tests that need Redis run only when TEST_REDIS_URL is set, and they FLUSH that
// Redis database. Use a throwaway DB index, e.g.:
//
//	TEST_REDIS_URL=redis://localhost:6379/15 go test ./...
//
// Without TEST_REDIS_URL those tests are skipped; the pure logic tests always run.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func setLimits(t *testing.T, user, ip, sensitive int64, trusted string) {
	t.Helper()
	nets, err := parseTrustedProxies(trusted)
	if err != nil {
		t.Fatalf("parseTrustedProxies: %v", err)
	}
	rlConfig = rateLimitConfig{window: time.Minute, userLimit: user, ipLimit: ip, sensitiveLimit: sensitive, trustedProxies: nets}
}

func useTestRedis(t *testing.T) {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set; skipping Redis-backed test")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("TEST_REDIS_URL: %v", err)
	}
	redisClient = redis.NewClient(opt)
	if err := redisClient.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test redis: %v", err)
	}
	t.Cleanup(func() { redisClient.Close(); redisClient = nil })
}

// fakeUpstream stands in for order-service so proxied requests return 200.
func fakeUpstream(t *testing.T) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(up.Close)
	routes = map[string]string{"/api/orders": up.URL, "/api/shipping": up.URL}
}

func tokenFor(t *testing.T, email string) string {
	t.Helper()
	jwtSecret = []byte("test-secret")
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": email, "role": "customer", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func request(method, path, remoteAddr string, body string) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.RemoteAddr = remoteAddr
	return r
}

// ---- R1: client identity ----------------------------------------------------

func TestClientIPIgnoresSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	setLimits(t, 100, 100, 100, "")
	r := request("GET", "/", "203.0.113.7:51234", "")
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("want direct peer IP 203.0.113.7, got %q", got)
	}
}

func TestClientIPStripsPort(t *testing.T) {
	setLimits(t, 100, 100, 100, "")
	for addr, want := range map[string]string{
		"198.51.100.1:40000": "198.51.100.1",
		"198.51.100.1:40001": "198.51.100.1", // new connection, same client
		"[2001:db8::1]:443":  "2001:db8::1",
	} {
		if got := clientIP(request("GET", "/", addr, "")); got != want {
			t.Errorf("%s: want %q, got %q", addr, want, got)
		}
	}
}

func TestClientIPReadsForwardedChainOnlyFromTrustedProxy(t *testing.T) {
	setLimits(t, 100, 100, 100, "10.0.0.0/8")
	// Client forged 1.2.3.4 at the left; our proxies appended the real client and themselves.
	r := request("GET", "/", "10.0.1.5:40000", "")
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.9, 10.0.2.2")
	if got := clientIP(r); got != "198.51.100.9" {
		t.Fatalf("want right-most untrusted 198.51.100.9, got %q", got)
	}

	r = request("GET", "/", "10.0.1.5:40000", "")
	r.Header.Set("X-Real-IP", "198.51.100.10")
	if got := clientIP(r); got != "198.51.100.10" {
		t.Fatalf("want X-Real-IP from trusted proxy, got %q", got)
	}
}

// ---- R2: authenticated users limited per user --------------------------------

func TestAuthenticatedUsersAreLimitedPerUser(t *testing.T) {
	useTestRedis(t)
	fakeUpstream(t)
	setLimits(t, 3, 100, 100, "")
	alice := tokenFor(t, "alice@test.com")
	bob := tokenFor(t, "bob@test.com")

	before := testutil.ToFloat64(rateLimitDecisions.WithLabelValues(scopeUser, "limited"))
	// Alice from different IPs and ports: still one budget, because the key is her identity.
	for i := 1; i <= 4; i++ {
		r := request("GET", "/api/orders/", fmt.Sprintf("198.51.100.%d:%d", i, 40000+i), "")
		r.Header.Set("Authorization", "Bearer "+alice)
		rec := httptest.NewRecorder()
		handleProxy(rec, r)
		want := http.StatusOK
		if i == 4 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("alice request %d: want %d, got %d", i, want, rec.Code)
		}
	}
	// Bob shares an IP with Alice's last request but has his own budget.
	r := request("GET", "/api/orders/", "198.51.100.4:50000", "")
	r.Header.Set("Authorization", "Bearer "+bob)
	rec := httptest.NewRecorder()
	handleProxy(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob should not be limited by alice's usage, got %d", rec.Code)
	}
	if got := testutil.ToFloat64(rateLimitDecisions.WithLabelValues(scopeUser, "limited")) - before; got != 1 {
		t.Fatalf("want 1 limited decision recorded, got %v", got)
	}
}

// ---- R3: sensitive anonymous routes limited per IP, more strictly ------------

func TestSensitiveRoutesAreLimitedPerIPWithStricterLimit(t *testing.T) {
	useTestRedis(t)
	fakeUpstream(t)
	setLimits(t, 100, 100, 2, "")

	// Login: different emails and ports from one IP share the stricter budget.
	codes := []int{}
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		handleLogin(rec, request("POST", "/auth/login", fmt.Sprintf("203.0.113.5:%d", 40000+i),
			fmt.Sprintf(`{"email":"user%d@test.com","password":"x"}`, i)))
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("login from one IP: want [200 200 429], got %v", codes)
	}
	// A different IP is unaffected.
	rec := httptest.NewRecorder()
	handleLogin(rec, request("POST", "/auth/login", "203.0.113.6:40000", `{"email":"x@test.com","password":"x"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("login from another IP: want 200, got %d", rec.Code)
	}

	// Webhook is sensitive too.
	codes = codes[:0]
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		handleProxy(rec, request("POST", "/api/shipping/webhook", "203.0.113.9:1", `{}`))
		codes = append(codes, rec.Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("webhook from one IP: want third request 429, got %v", codes)
	}
}

// ---- R4: atomic counter that always expires -----------------------------------

func TestCounterIsAtomicAndAlwaysHasExpiry(t *testing.T) {
	useTestRedis(t)
	setLimits(t, 1000, 1000, 1000, "")

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowRequest(context.Background(), scopeUser, "carol@test.com", 1000)
		}()
	}
	wg.Wait()

	key := "rl:" + scopeUser + ":" + hashID("carol@test.com")
	n, _ := redisClient.Get(context.Background(), key).Int()
	ttl, _ := redisClient.PTTL(context.Background(), key).Result()
	if n != 200 || ttl <= 0 {
		t.Fatalf("want count 200 with an expiry, got count %d ttl %v", n, ttl)
	}

	// A key left without expiry (the old INCR/EXPIRE crash gap) gets repaired.
	stuck := "rl:" + scopeUser + ":" + hashID("stuck@test.com")
	redisClient.Set(context.Background(), stuck, 5000, 0)
	allowRequest(context.Background(), scopeUser, "stuck@test.com", 10)
	if ttl, _ := redisClient.PTTL(context.Background(), stuck).Result(); ttl <= 0 {
		t.Fatalf("stuck key should get an expiry, ttl %v", ttl)
	}
}

// ---- R5: 429 with Retry-After --------------------------------------------------

func TestBlockedResponseHasRetryAfter(t *testing.T) {
	useTestRedis(t)
	setLimits(t, 100, 100, 1, "")
	handleLogin(httptest.NewRecorder(), request("POST", "/auth/login", "203.0.113.20:1", `{"email":"a@b.c","password":"x"}`))

	rec := httptest.NewRecorder()
	handleLogin(rec, request("POST", "/auth/login", "203.0.113.20:2", `{"email":"a@b.c","password":"x"}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 60 {
		t.Fatalf("want Retry-After between 1 and 60 seconds, got %q", rec.Header().Get("Retry-After"))
	}
}

// ---- R6: configuration from environment ---------------------------------------

func TestConfigComesFromEnvironment(t *testing.T) {
	t.Setenv("RATE_LIMIT_WINDOW_SECONDS", "30")
	t.Setenv("RATE_LIMIT_USER_PER_WINDOW", "200")
	t.Setenv("RATE_LIMIT_IP_PER_WINDOW", "50")
	t.Setenv("RATE_LIMIT_SENSITIVE_PER_WINDOW", "5")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/16, 192.168.1.10")
	cfg, err := loadRateLimitConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.window != 30*time.Second || cfg.userLimit != 200 || cfg.ipLimit != 50 ||
		cfg.sensitiveLimit != 5 || len(cfg.trustedProxies) != 2 {
		t.Fatalf("config not read from env: %+v", cfg)
	}

	t.Setenv("RATE_LIMIT_USER_PER_WINDOW", "lots")
	if _, err := loadRateLimitConfig(); err == nil {
		t.Fatal("want an error for a non-numeric limit")
	}
	t.Setenv("RATE_LIMIT_USER_PER_WINDOW", "200")
	t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")
	if _, err := loadRateLimitConfig(); err == nil {
		t.Fatal("want an error for an invalid trusted proxy entry")
	}
}

func TestConfigDefaultsWhenUnset(t *testing.T) {
	for _, k := range []string{"RATE_LIMIT_WINDOW_SECONDS", "RATE_LIMIT_USER_PER_WINDOW", "RATE_LIMIT_IP_PER_WINDOW",
		"RATE_LIMIT_SENSITIVE_PER_WINDOW", "TRUSTED_PROXY_CIDRS"} {
		t.Setenv(k, "")
	}
	cfg, err := loadRateLimitConfig()
	if err != nil || cfg.window != time.Minute || cfg.userLimit != 120 || cfg.ipLimit != 60 ||
		cfg.sensitiveLimit != 20 || len(cfg.trustedProxies) != 0 {
		t.Fatalf("unexpected defaults: %+v (err %v)", cfg, err)
	}
}

// ---- R7: fail open when Redis is unavailable -----------------------------------

func TestFailsOpenWhenRedisIsDown(t *testing.T) {
	fakeUpstream(t)
	setLimits(t, 1, 1, 1, "")
	redisClient = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) // nothing listens here
	t.Cleanup(func() { redisClient.Close(); redisClient = nil })

	before := testutil.ToFloat64(rateLimitDecisions.WithLabelValues(scopeUser, "redis_error"))
	for i := 0; i < 3; i++ { // above the limit of 1, yet all must pass
		r := request("GET", "/api/orders/", "198.51.100.1:1", "")
		r.Header.Set("Authorization", "Bearer "+tokenFor(t, "dave@test.com"))
		rec := httptest.NewRecorder()
		handleProxy(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200 (fail open), got %d", i+1, rec.Code)
		}
	}
	if got := testutil.ToFloat64(rateLimitDecisions.WithLabelValues(scopeUser, "redis_error")) - before; got != 3 {
		t.Fatalf("want 3 redis_error decisions recorded, got %v", got)
	}
}
