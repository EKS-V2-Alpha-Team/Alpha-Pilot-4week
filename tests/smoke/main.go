// Smoke test for the order platform: runs one real order flow through the public
// entry point and prints PASS / FAIL / WARN per step. Run from the repo root:
//
//	go run tests/smoke/main.go -base http://localhost:8080
//	go run tests/smoke/main.go -base https://dashboard.dev.eksv2.naimaosmann.com -insecure
//
// Login accepts any email and password today (a known risk), so the flags below are only
// sent along; they start to matter when real login lands.
//
// Stock numbers are compared with a baseline, so other people ordering the same product
// during the run can make a stock step fail; rerun when the platform is quiet.
// It creates real data: two orders (one taken to delivered, one cancelled), so stock,
// payments, shipments and emails change. Run it against dev or local, never prod.
// Exit code is 1 if any step fails.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	base     = flag.String("base", "http://localhost:8080", "public URL of the platform (no trailing slash)")
	email    = flag.String("email", "admin@platform.local", "login email (any value works today)")
	password = flag.String("password", "admin", "login password (any value works today)")
	wait     = flag.Duration("wait", 60*time.Second, "how long to wait for each async step (worker, SQS)")
	insecure = flag.Bool("insecure", false, "skip TLS certificate checks (dev only: Let's Encrypt staging certs are not trusted)")

	client = &http.Client{Timeout: 15 * time.Second}
	token  string
	failed int
)

type product struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Stock    int     `json:"stock"`
	Reserved int     `json:"reserved"`
}

type order struct {
	ID     int     `json:"id"`
	Status string  `json:"status"`
	Total  float64 `json:"total"`
}

func main() {
	flag.Parse()
	*base = strings.TrimSuffix(*base, "/")
	if *insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		fmt.Println("WARNING: TLS certificate checks are off (-insecure)")
	}
	// A redirect would turn POST into GET and hide the real answer, so never follow one.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	fmt.Printf("Smoke test against %s  (%s)\n\n", *base, time.Now().Format(time.RFC3339))

	// 1. Health of every service through the gateway.
	for _, svc := range []string{"orders", "inventory", "payments", "notifications", "shipping"} {
		code, _, err := call("GET", "/api/"+svc+"/healthz", nil, nil)
		check(err == nil && code == 200, "health: "+svc, fmt.Sprintf("HTTP %d %v", code, errStr(err)))
	}

	// 2. Log in as the admin.
	code, body, err := call("POST", "/auth/login", map[string]string{"email": *email, "password": *password}, nil)
	var login struct{ Token string }
	_ = json.Unmarshal(body, &login)
	token = login.Token
	if !check(err == nil && code == 200 && token != "", "login", fmt.Sprintf("HTTP %d %v", code, errStr(err))) {
		finish()
	}

	// 3. Pick a product with at least 2 available.
	p, ok := pickProduct()
	if !check(ok, "pick a product with stock", p.ID) {
		finish()
	}
	fmt.Printf("      using %s (%s): stock %d, reserved %d\n", p.ID, p.Name, p.Stock, p.Reserved)

	// 4. Place an order with an Idempotency-Key.
	key := newKey()
	req := map[string]interface{}{
		"items": []map[string]interface{}{{"product_id": p.ID, "quantity": 1}},
	}
	code, body, err = call("POST", "/api/orders/", req, map[string]string{"Idempotency-Key": key})
	var o order
	_ = json.Unmarshal(body, &o)
	if !check(err == nil && code == 201 && o.ID > 0, "place order", fmt.Sprintf("HTTP %d, order #%d %v %s", code, o.ID, errStr(err), short(body))) {
		finish()
	}

	// 5. Same key, same request again: must return the same order, not a new one.
	code, body, err = call("POST", "/api/orders/", req, map[string]string{"Idempotency-Key": key})
	var again order
	_ = json.Unmarshal(body, &again)
	switch {
	case err == nil && code == 200 && again.ID == o.ID:
		pass("retry with same Idempotency-Key returns the same order", fmt.Sprintf("#%d", o.ID))
	case code == 201 && again.ID != o.ID:
		fail("retry with same Idempotency-Key returns the same order", fmt.Sprintf("a second order #%d was created (idempotency not deployed); cancelling it", again.ID))
		_, _, _ = call("PUT", "/api/orders/status", map[string]interface{}{"order_id": again.ID, "new_status": "cancelled"}, nil)
	default:
		fail("retry with same Idempotency-Key returns the same order", fmt.Sprintf("HTTP %d %v %s", code, errStr(err), short(body)))
	}

	// 6. The worker reserves, charges and confirms. The payment simulator declines about
	// 1 in 10 charges on purpose; a declined order is checked and replaced with a new one.
	o, ok = confirmOrRetry(o, p, req)
	if !ok {
		finish()
	}

	// 7. Stock committed: stock down by 1, reserved back where it was.
	okInv := waitFor(func() (bool, string) {
		q, found := getProduct(p.ID)
		if !found {
			return false, "product not found"
		}
		return q.Stock == p.Stock-1 && q.Reserved == p.Reserved,
			fmt.Sprintf("stock %d -> %d (want %d), reserved %d -> %d (want %d)", p.Stock, q.Stock, p.Stock-1, p.Reserved, q.Reserved, p.Reserved)
	})
	check(okInv.ok, "stock committed (stock -1, reserved back to before)", okInv.detail)

	// 8. Charge in the ledger and the confirmation email.
	r := waitFor(func() (bool, string) { return ledgerHas(o.ID, "charge"), "" })
	check(r.ok, "payment charge in the ledger", fmt.Sprintf("Order #%d payment", o.ID))
	r = waitFor(func() (bool, string) { return emailSent(o.ID, "order_confirmed"), "" })
	check(r.ok, "order_confirmed email", "")

	// 9. Walk the order to delivered the way the dashboard does.
	for _, next := range []string{"processing", "shipped", "delivered"} {
		code, body, err = call("PUT", "/api/orders/status", map[string]interface{}{"order_id": o.ID, "new_status": next}, nil)
		check(err == nil && code == 200, "advance to "+next, fmt.Sprintf("HTTP %d %v %s", code, errStr(err), short(body)))
	}
	r = waitFor(func() (bool, string) {
		s, ok := shipmentStatus(o.ID)
		return ok && s == "delivered", "shipment status: " + s
	})
	check(r.ok, "shipment created and delivered", r.detail)
	r = waitFor(func() (bool, string) { return emailSent(o.ID, "order_shipped"), "" })
	check(r.ok, "order_shipped email", "")
	r = waitFor(func() (bool, string) { return emailSent(o.ID, "order_delivered"), "" })
	check(r.ok, "order_delivered email", "")

	// 10. A second order, cancelled after payment: refund, stock back, cancel email.
	before, _ := getProduct(p.ID)
	code, body, err = call("POST", "/api/orders/", req, map[string]string{"Idempotency-Key": newKey()})
	var c order
	_ = json.Unmarshal(body, &c)
	if check(err == nil && code == 201 && c.ID > 0, "place second order (to cancel)", fmt.Sprintf("HTTP %d, order #%d", code, c.ID)) {
		var ok bool
		if c, ok = confirmOrRetry(c, before, req); ok {
			code, body, err = call("PUT", "/api/orders/status", map[string]interface{}{"order_id": c.ID, "new_status": "cancelled"}, nil)
			check(err == nil && code == 200, "cancel the paid order", fmt.Sprintf("HTTP %d %v %s", code, errStr(err), short(body)))
			r = waitFor(func() (bool, string) { return ledgerHas(c.ID, "refund"), "" })
			check(r.ok, "refund in the ledger", fmt.Sprintf("Order #%d refund", c.ID))
			r = waitFor(func() (bool, string) {
				q, _ := getProduct(p.ID)
				return q.Stock == before.Stock && q.Reserved == before.Reserved,
					fmt.Sprintf("stock %d (want %d), reserved %d (want %d)", q.Stock, before.Stock, q.Reserved, before.Reserved)
			})
			check(r.ok, "stock returned after cancel", r.detail)
			r = waitFor(func() (bool, string) { return emailSent(c.ID, "order_cancelled"), "" })
			check(r.ok, "order_cancelled email", "")
		}
	}

	// 11. Internal routes must not be reachable from outside (404 at the gateway).
	// The probe names order -1 and no items, which no real order or product matches, so an
	// unpatched service changes nothing (reserve and restock still answer 201/200 there).
	for _, rt := range [][2]string{{"POST", "/api/payments/charge"}, {"POST", "/api/payments/refund"}, {"POST", "/api/inventory/reserve"}, {"POST", "/api/inventory/restock"}, {"POST", "/api/shipping/shipments"}, {"POST", "/api/notifications/send"}} {
		code, _, err = call(rt[0], rt[1], map[string]interface{}{"order_id": -1}, nil)
		check(err == nil && code == 404, "blocked from outside: "+rt[0]+" "+rt[1], fmt.Sprintf("HTTP %d %v", code, errStr(err)))
	}

	finish()
}

// ---- checks ----------------------------------------------------------------------------

type result struct {
	ok     bool
	detail string
}

func waitFor(f func() (bool, string)) result {
	deadline := time.Now().Add(*wait)
	var detail string
	for {
		ok, d := f()
		detail = d
		if ok || time.Now().After(deadline) {
			return result{ok, detail}
		}
		time.Sleep(2 * time.Second)
	}
}

// confirmOrRetry waits for the order to be confirmed. If the payment simulator declined
// it, it checks the decline path (cancelled, stock back to the baseline taken before the
// order was placed) and tries a new order, up to 3 attempts in total.
func confirmOrRetry(o order, before product, req interface{}) (order, bool) {
	for attempt := 1; ; attempt++ {
		st, ok := waitStatus(o.ID, "confirmed")
		if ok {
			pass("order confirmed by the worker (reserve -> charge -> commit)", fmt.Sprintf("#%d", o.ID))
			return o, true
		}
		if st != "cancelled" || attempt == 3 {
			fail("order confirmed by the worker (reserve -> charge -> commit)", fmt.Sprintf("#%d last status: %s", o.ID, st))
			return o, false
		}
		// Declined: stock must be released (reserved back down, stock unchanged).
		r := waitFor(func() (bool, string) {
			q, _ := getProduct(before.ID)
			return q.Reserved == before.Reserved && q.Stock == before.Stock,
				fmt.Sprintf("stock %d, reserved %d", q.Stock, q.Reserved)
		})
		check(r.ok, fmt.Sprintf("declined payment on #%d: cancelled and stock released", o.ID), r.detail)
		if emailSent(o.ID, "payment_failed") {
			pass("payment_failed email", "")
		} else {
			fmt.Printf("WARN  %-58s %s\n", "payment_failed email", "not seen (best effort by design)")
		}
		code, body, err := call("POST", "/api/orders/", req, map[string]string{"Idempotency-Key": newKey()})
		var n order
		_ = json.Unmarshal(body, &n)
		if !check(err == nil && code == 201 && n.ID > 0, "place a replacement order", fmt.Sprintf("HTTP %d, order #%d", code, n.ID)) {
			return o, false
		}
		o = n
	}
}

func waitStatus(id int, want string) (string, bool) {
	var last string
	r := waitFor(func() (bool, string) {
		code, body, err := call("GET", fmt.Sprintf("/api/orders/%d", id), nil, nil)
		if err != nil || code != 200 {
			return false, fmt.Sprintf("HTTP %d %v", code, errStr(err))
		}
		var o order
		_ = json.Unmarshal(body, &o)
		last = o.Status
		return o.Status == want || o.Status == "cancelled", o.Status
	})
	return last, r.ok && last == want
}

func pickProduct() (product, bool) {
	code, body, err := call("GET", "/api/inventory/products", nil, nil)
	if err != nil || code != 200 {
		return product{ID: fmt.Sprintf("HTTP %d %v", code, errStr(err))}, false
	}
	var ps []product
	_ = json.Unmarshal(body, &ps)
	for _, p := range ps {
		if p.Stock-p.Reserved >= 2 && p.Price > 0 {
			return p, true
		}
	}
	return product{ID: "no product has 2 or more available"}, false
}

func getProduct(id string) (product, bool) {
	code, body, err := call("GET", "/api/inventory/products/"+id, nil, nil)
	if err != nil || code != 200 {
		return product{}, false
	}
	var p product
	_ = json.Unmarshal(body, &p)
	return p, p.ID != ""
}

func ledgerHas(orderID int, entryType string) bool {
	code, body, err := call("GET", "/api/payments/ledger", nil, nil)
	if err != nil || code != 200 {
		return false
	}
	var es []struct {
		EntryType   string `json:"entry_type"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(body, &es)
	prefix := fmt.Sprintf("Order #%d ", orderID)
	for _, e := range es {
		if e.EntryType == entryType && strings.HasPrefix(e.Description, prefix) {
			return true
		}
	}
	return false
}

func emailSent(orderID int, template string) bool {
	code, body, err := call("GET", "/api/notifications/history", nil, nil)
	if err != nil || code != 200 {
		return false
	}
	var ns []struct {
		Template string `json:"template"`
		Subject  string `json:"subject"`
	}
	_ = json.Unmarshal(body, &ns)
	suffix := fmt.Sprintf("#%d", orderID)
	for _, n := range ns {
		if n.Template == template && strings.HasSuffix(n.Subject, suffix) {
			return true
		}
	}
	return false
}

func shipmentRecipient(orderID int) (string, string) {
	_, body, err := call("GET", "/api/shipping/shipments", nil, nil)
	if err != nil {
		return "", ""
	}
	var ss []struct {
		OrderID       int    `json:"order_id"`
		RecipientName string `json:"recipient_name"`
		City          string `json:"city"`
	}
	_ = json.Unmarshal(body, &ss)
	for _, s := range ss {
		if s.OrderID == orderID {
			return s.RecipientName, s.City
		}
	}
	return "", ""
}

func shipmentStatus(orderID int) (string, bool) {
	code, body, err := call("GET", "/api/shipping/shipments", nil, nil)
	if err != nil || code != 200 {
		return fmt.Sprintf("HTTP %d", code), false
	}
	var ss []struct {
		OrderID int    `json:"order_id"`
		Status  string `json:"status"`
	}
	_ = json.Unmarshal(body, &ss)
	for _, s := range ss {
		if s.OrderID == orderID {
			return s.Status, true
		}
	}
	return "none", false
}

// ---- http --------------------------------------------------------------------------------

func call(method, path string, payload interface{}, extra map[string]string) (int, []byte, error) {
	for attempt := 1; ; attempt++ {
		code, body, err := callOnce(method, path, payload, extra)
		if code != 429 || attempt == 3 {
			return code, body, err
		}
		// Rate limited (a dashboard tab shares the same user): wait and retry.
		time.Sleep(15 * time.Second)
	}
}

func callOnce(method, path string, payload interface{}, extra map[string]string) (int, []byte, error) {
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, *base+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "smoke-" + hex.EncodeToString(b)
}

// ---- output ------------------------------------------------------------------------------

func check(ok bool, name, detail string) bool {
	if ok {
		pass(name, detail)
	} else {
		fail(name, detail)
	}
	return ok
}

func pass(name, detail string) { fmt.Printf("PASS  %-58s %s\n", name, detail) }

func fail(name, detail string) {
	failed++
	fmt.Printf("FAIL  %-58s %s\n", name, detail)
}

func finish() {
	if failed > 0 {
		fmt.Printf("\n%d step(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nall steps passed")
	os.Exit(0)
}

func short(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
