package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	redisClient *redis.Client
	ctx         = context.Background()
	jwtSecret   []byte
	routes      map[string]string
	rlConfig    rateLimitConfig
)

func main() {
	jwtSecret = []byte(getEnv("JWT_SECRET", "change-me-in-production"))

	var err error
	rlConfig, err = loadRateLimitConfig()
	if err != nil {
		log.Fatalf("Invalid rate limit configuration: %v", err)
	}

	// Service routes - internal service URLs
	routes = map[string]string{
		"/api/orders":        getEnv("ORDER_SERVICE_URL", "http://order-service:8081"),
		"/api/inventory":     getEnv("INVENTORY_SERVICE_URL", "http://inventory-service:8082"),
		"/api/payments":      getEnv("PAYMENT_SERVICE_URL", "http://payment-service:8083"),
		"/api/notifications": getEnv("NOTIFICATION_SERVICE_URL", "http://notification-service:8084"),
		"/api/shipping":      getEnv("SHIPPING_SERVICE_URL", "http://shipping-service:8085"),
		"/api/dashboard":     getEnv("DASHBOARD_SERVICE_URL", "http://dashboard-api:8086"),
		// The dashboard page calls /dashboard/* on the gateway's own origin.
		"/dashboard/": getEnv("DASHBOARD_SERVICE_URL", "http://dashboard-api:8086"),
	}

	// Redis for rate limiting. The client is kept even if Redis is not reachable yet:
	// go-redis reconnects on its own, and each request fails open until Redis is back.
	// (Previously a failed startup ping disabled rate limiting until the next restart.)
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("Invalid REDIS_URL: %v", err)
		}
		redisClient = redis.NewClient(opt)
		if _, err := redisClient.Ping(ctx).Result(); err != nil {
			log.Printf("WARNING: Redis not reachable yet, rate limiting fails open until it is: %v", err)
		} else {
			log.Println("Redis connected for rate limiting")
		}
	} else {
		log.Println("WARNING: REDIS_URL not set, rate limiting disabled")
	}
	log.Printf("Rate limits per %s: user=%d ip=%d sensitive=%d, trusted proxies=%d",
		rlConfig.window, rlConfig.userLimit, rlConfig.ipLimit, rlConfig.sensitiveLimit, len(rlConfig.trustedProxies))

	startMetricsServer(getEnv("METRICS_PORT", "9090"))

	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/auth/login", handleLogin)
	mux.HandleFunc("/auth/register", handleRegister)
	mux.HandleFunc("/", handleProxy)

	port := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go gracefulShutdown(server)

	log.Printf("API Gateway listening on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "api-gateway"})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Login is a brute-force target: stricter per-IP limit, applied before any work.
	if rateLimited(w, r, scopeSensitive, clientIP(r), rlConfig.sensitiveLimit) {
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// In production this would validate against a user database
	// For this project, accept any email/password and return a JWT
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  req.Email,
		"role": "customer",
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	})

	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		httpError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if rateLimited(w, r, scopeSensitive, clientIP(r), rlConfig.sensitiveLimit) {
		return
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" {
		httpError(w, "email and password required", http.StatusBadRequest)
		return
	}

	// Return a JWT for the new user
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  req.Email,
		"name": req.Name,
		"role": "customer",
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	})

	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		httpError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "registered",
		"email":   req.Email,
		"token":   tokenString,
	})
}

func handleProxy(w http.ResponseWriter, r *http.Request) {
	if isPublicPath(r.URL.Path) {
		// Anonymous traffic is limited per client IP; sensitive routes (webhook) more strictly.
		scope, limit := scopeIP, rlConfig.ipLimit
		if isSensitivePath(r.URL.Path) {
			scope, limit = scopeSensitive, rlConfig.sensitiveLimit
		}
		if rateLimited(w, r, scope, clientIP(r), limit) {
			return
		}
	} else {
		claims, err := validateToken(r)
		if err != nil {
			httpError(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		// Logged-in shoppers are limited per user, so many shoppers behind one
		// IP (mobile carrier, office NAT) don't share a single budget.
		sub, _ := claims["sub"].(string)
		id := sub
		if id == "" {
			id = "ip:" + clientIP(r) // token without a subject: fall back to the IP
		}
		if rateLimited(w, r, scopeUser, id, rlConfig.userLimit) {
			return
		}
		// Forward user info to downstream services
		if sub != "" {
			r.Header.Set("X-User-Email", sub)
		}
		if role, ok := claims["role"].(string); ok {
			r.Header.Set("X-User-Role", role)
		}
	}

	// Find matching route
	for prefix, targetURL := range routes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			// Strip the route prefix for downstream. dashboard-api serves /dashboard/*
			// under that exact path, so that route keeps it.
			if prefix != "/dashboard/" {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
			}
			forward(w, r, targetURL)
			return
		}
	}

	// The dashboard page itself, served by dashboard-api at "/".
	if r.URL.Path == "/" {
		forward(w, r, routes["/dashboard/"])
		return
	}

	httpError(w, "not found", http.StatusNotFound)
}

func forward(w http.ResponseWriter, r *http.Request, targetURL string) {
	target, err := url.Parse(targetURL)
	if err != nil {
		httpError(w, "bad upstream config", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error: %v", err)
		httpError(w, "service unavailable", http.StatusBadGateway)
	}

	r.Header.Set("X-Forwarded-For", r.RemoteAddr)
	r.Header.Set("X-Request-ID", fmt.Sprintf("%d", time.Now().UnixNano()))

	proxy.ServeHTTP(w, r)
}

func isPublicPath(path string) bool {
	// The dashboard page is static HTML; its data calls still need a token.
	if path == "/" {
		return true
	}
	public := []string{"/healthz", "/auth/", "/api/shipping/webhook"}
	for _, p := range public {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	// Allow health checks through to downstream services
	if strings.HasSuffix(path, "/healthz") {
		return true
	}
	return false
}

// isSensitivePath marks anonymous routes that attackers target (credential
// stuffing, account creation, forged carrier callbacks). They get the stricter limit.
func isSensitivePath(path string) bool {
	return strings.HasPrefix(path, "/auth/") || strings.HasPrefix(path, "/api/shipping/webhook")
}

func validateToken(r *http.Request) (jwt.MapClaims, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}

	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, fmt.Errorf("invalid Authorization format")
	}

	token, err := jwt.Parse(parts[1], func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func httpError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Rate limiting
//
// Interim design (decided 24 Sep, revisit at the hardening pass): all limiting
// lives in this gateway. Fixed window per key, counted atomically in Redis.
// Fails open if Redis is unavailable, with throttled structured logs and metrics.
// Later option once Traefik runs on EKS: per-IP limits at the edge (Traefik
// RateLimit with Redis), per-user limits stay here because only the gateway
// knows the user.
// ---------------------------------------------------------------------------

const (
	scopeUser      = "user"      // authenticated traffic, keyed by JWT subject
	scopeIP        = "ip"        // anonymous traffic, keyed by client IP
	scopeSensitive = "sensitive" // login/register/webhook, keyed by client IP, stricter
)

// Each Redis call gets a short deadline so a slow Redis cannot stall requests;
// on timeout the request fails open.
const redisTimeout = 100 * time.Millisecond

type rateLimitConfig struct {
	window         time.Duration
	userLimit      int64
	ipLimit        int64
	sensitiveLimit int64
	trustedProxies []*net.IPNet
}

// loadRateLimitConfig reads limits and trusted proxies from the environment.
// Defaults are starting points to tune from load-test results, not measured values.
func loadRateLimitConfig() (rateLimitConfig, error) {
	var cfg rateLimitConfig
	windowSecs, err := envInt("RATE_LIMIT_WINDOW_SECONDS", 60)
	if err != nil {
		return cfg, err
	}
	cfg.window = time.Duration(windowSecs) * time.Second
	if cfg.userLimit, err = envInt("RATE_LIMIT_USER_PER_WINDOW", 120); err != nil {
		return cfg, err
	}
	if cfg.ipLimit, err = envInt("RATE_LIMIT_IP_PER_WINDOW", 60); err != nil {
		return cfg, err
	}
	if cfg.sensitiveLimit, err = envInt("RATE_LIMIT_SENSITIVE_PER_WINDOW", 20); err != nil {
		return cfg, err
	}
	cfg.trustedProxies, err = parseTrustedProxies(os.Getenv("TRUSTED_PROXY_CIDRS"))
	return cfg, err
}

func envInt(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, v)
	}
	return n, nil
}

// parseTrustedProxies accepts a comma-separated list of CIDRs or single IPs,
// e.g. "10.0.0.0/16,192.168.1.10". Empty means no proxy is trusted.
func parseTrustedProxies(s string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			if ip := net.ParseIP(part); ip != nil && ip.To4() != nil {
				part += "/32"
			} else {
				part += "/128"
			}
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS: invalid entry %q: %w", part, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

func isTrustedProxy(ip net.IP) bool {
	for _, n := range rlConfig.trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the caller's IP without the port.
// Forwarding headers are only believed when the direct peer is a trusted proxy;
// otherwise any client could claim any IP. With a trusted proxy, X-Forwarded-For
// is read right to left (the right end is written by our own proxies) and the
// first address that is not a trusted proxy is the real client.
func clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	remoteIP := net.ParseIP(remote)
	if remoteIP == nil || !isTrustedProxy(remoteIP) {
		return remote
	}

	if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				return remote // malformed chain: don't guess
			}
			if !isTrustedProxy(ip) {
				return ip.String()
			}
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return remote
}

// rateLimitScript increments the counter and sets its expiry in one atomic step.
// It also repairs keys that somehow have no expiry (PTTL = -1), so no client can
// be blocked forever. Returns {count, milliseconds until the window resets}.
var rateLimitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {count, ttl}
`)

var errLimiterDisabled = errors.New("rate limiter disabled: REDIS_URL not set")

// allowRequest counts one request for (scope, id) and reports whether it is within limit.
func allowRequest(c context.Context, scope, id string, limit int64) (bool, time.Duration, error) {
	if redisClient == nil {
		return true, 0, errLimiterDisabled
	}
	key := "rl:" + scope + ":" + hashID(id)
	res, err := rateLimitScript.Run(c, redisClient, []string{key}, rlConfig.window.Milliseconds()).Slice()
	if err != nil {
		return true, 0, err
	}
	if len(res) != 2 {
		return true, 0, fmt.Errorf("unexpected rate limit script result: %v", res)
	}
	count, _ := res[0].(int64)
	ttlMs, _ := res[1].(int64)
	if count > limit {
		return false, time.Duration(ttlMs) * time.Millisecond, nil
	}
	return true, 0, nil
}

// rateLimited applies the limit and writes a 429 with Retry-After when blocked.
// It returns true when the request was blocked and the caller must stop.
func rateLimited(w http.ResponseWriter, r *http.Request, scope, id string, limit int64) bool {
	c, cancel := context.WithTimeout(r.Context(), redisTimeout)
	defer cancel()

	allowed, retryAfter, err := allowRequest(c, scope, id, limit)
	switch {
	case errors.Is(err, errLimiterDisabled):
		rateLimitDecisions.WithLabelValues(scope, "disabled").Inc()
		return false
	case err != nil:
		rateLimitDecisions.WithLabelValues(scope, "redis_error").Inc()
		logLimiterFailure(scope, err)
		return false // fail open: keep the shop working
	case !allowed:
		rateLimitDecisions.WithLabelValues(scope, "limited").Inc()
		secs := int(math.Ceil(retryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		limiterLog.Warn("rate limit exceeded",
			"scope", scope, "key", hashID(id), "limit", limit,
			"window_s", int(rlConfig.window.Seconds()), "retry_after_s", secs, "path", r.URL.Path)
		httpError(w, "rate limit exceeded", http.StatusTooManyRequests)
		return true
	default:
		rateLimitDecisions.WithLabelValues(scope, "allowed").Inc()
		return false
	}
}

// hashID keeps emails and IPs out of Redis keys and logs (GDPR-style data minimisation).
func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}

// limiterLog emits JSON lines so rate-limit events are easy to query in a log backend.
var limiterLog = slog.New(slog.NewJSONHandler(os.Stderr, nil))

// logLimiterFailure logs Redis failures at most once per 10 s, with a count of
// suppressed failures, so an outage doesn't flood the logs at peak traffic.
var (
	lastLimiterErrLog     atomic.Int64
	suppressedLimiterErrs atomic.Int64
)

func logLimiterFailure(scope string, err error) {
	now := time.Now().Unix()
	last := lastLimiterErrLog.Load()
	if now-last < 10 || !lastLimiterErrLog.CompareAndSwap(last, now) {
		suppressedLimiterErrs.Add(1)
		return
	}
	limiterLog.Error("rate limiter unavailable, failing open",
		"scope", scope, "error", err.Error(), "suppressed_since_last_log", suppressedLimiterErrs.Swap(0))
}

// rateLimitDecisions counts every limiter decision. Alert on result="redis_error"
// (protection is off) and watch result="limited" per scope.
var rateLimitDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "gateway_rate_limit_decisions_total",
	Help: "Rate limit decisions by scope (user, ip, sensitive) and result (allowed, limited, redis_error, disabled).",
}, []string{"scope", "result"})

func init() {
	prometheus.MustRegister(rateLimitDecisions)
}

// startMetricsServer serves Prometheus metrics on a separate port that the
// gateway never routes to, so /metrics is not reachable from the internet.
func startMetricsServer(port string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Printf("Metrics listening on :%s", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Printf("metrics server error: %v", err)
		}
	}()
}

var shutdownOnce sync.Once

func gracefulShutdown(server *http.Server) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	shutdownOnce.Do(func() {
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	})
}
