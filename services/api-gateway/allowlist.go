package main

// Which downstream routes the gateway exposes.
//
// Every /api/<service>/<path> used to be forwarded for any logged-in user, including the
// routes that only other services should call: take payment, refund, reserve or restock
// stock, create or move shipments, send notifications. The gateway now forwards only the
// routes below. Everything else gets a 404 here and never reaches the service.
//
// Services still call each other directly on their cluster addresses (the worker does not
// go through the gateway), so closing these routes at the edge does not affect the order
// flow. If a new public endpoint is added to a service, add it here as well.

import "strings"

type routeRule struct {
	method  string
	pattern string // "/", "/products", "/products/*" (one segment), "/dashboard/**" (everything below)
}

// The path is what the service sees, i.e. with the /api/<service> prefix already removed.
var exposedRoutes = map[string][]routeRule{
	"/api/orders": {
		{"GET", "/"}, {"POST", "/"}, {"GET", "/*"}, // list, create, read one
		{"PUT", "/status"}, // the dashboard advances and cancels orders
		{"GET", "/healthz"},
	},
	"/api/inventory": {
		{"GET", "/products"}, {"POST", "/products"}, {"GET", "/products/*"}, {"GET", "/low-stock"},
		{"GET", "/healthz"},
	},
	"/api/payments": {
		{"GET", "/ledger"}, {"GET", "/balance/*"},
		{"GET", "/healthz"},
	},
	"/api/notifications": {
		{"GET", "/history"}, {"GET", "/templates"},
		{"GET", "/healthz"},
	},
	"/api/shipping": {
		{"GET", "/shipments"}, {"GET", "/shipments/*"}, {"GET", "/track/*"},
		{"POST", "/webhook"}, // carrier callbacks (public, strictly rate limited)
		{"GET", "/healthz"},
	},
	"/api/dashboard": {{"GET", "/**"}},
	"/dashboard/":    {{"GET", "/**"}},
}

// isExposed reports whether a request may be forwarded. prefix is the matched route
// ("/api/orders"), path is the path the service would receive.
func isExposed(prefix, method, path string) bool {
	rules, ok := exposedRoutes[prefix]
	if !ok {
		return false
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	segs := splitPath(path)
	if segs == nil {
		return false // empty segments or . / .. in the path: never forwarded
	}
	for _, rule := range rules {
		if rule.method == method && matchPattern(splitPath(rule.pattern), segs) {
			return true
		}
	}
	return false
}

// splitPath returns the path's segments, [] for "/", or nil for an unsafe path.
func splitPath(p string) []string {
	if p == "/" {
		return []string{}
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
	segs := strings.Split(trimmed, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil
		}
	}
	return segs
}

func matchPattern(pattern, segs []string) bool {
	for i, p := range pattern {
		if p == "**" {
			return true
		}
		if i >= len(segs) {
			return false
		}
		if p != "*" && p != segs[i] {
			return false
		}
	}
	return len(pattern) == len(segs)
}
