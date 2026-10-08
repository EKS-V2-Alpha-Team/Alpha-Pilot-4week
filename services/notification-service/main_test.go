package main

// Integration tests for notification templates and dedupe (ALPHA-78). They need a real,
// DISPOSABLE Postgres:
//
//	TEST_DATABASE_URL=postgres://app:localdev@localhost:5432/notification_test?sslmode=disable go test ./...
//
// WARNING: every test TRUNCATES notifications in that database.
// Without TEST_DATABASE_URL the tests that need a database are skipped.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database integration test")
	}
	var err error
	db, err = sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	db.SetMaxOpenConns(20)
	migrate()
	if _, err := db.Exec(`TRUNCATE notifications RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func send(t *testing.T, payload map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	handleSend(rec, httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body)))
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func countSent(t *testing.T, template string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE template = $1`, template).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRenderTemplateFillsPlaceholders(t *testing.T) {
	got := renderTemplate("Hi {{.CustomerName}}, order #{{.OrderID}}: {{.Currency}} {{.Total}}",
		map[string]interface{}{"CustomerName": "sara", "OrderID": 42, "Currency": "GBP", "Total": 239.97})
	want := "Hi sara, order #42: GBP 239.97"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplateMissingValueIsBlank(t *testing.T) {
	got := renderTemplate("Tracking: {{.TrackingNumber}}", map[string]interface{}{})
	if got != "Tracking: " {
		t.Errorf("got %q, want blank after the label", got)
	}
}

func TestRenderTemplateBrokenTemplateIsReturnedAsWritten(t *testing.T) {
	got := renderTemplate("Hi {{.Name", nil)
	if got != "Hi {{.Name" {
		t.Errorf("got %q, want the template unchanged", got)
	}
}

func TestSendRendersTheStoredTemplate(t *testing.T) {
	setupTestDB(t)
	code, _ := send(t, map[string]interface{}{
		"recipient": "sara@example.com", "template": "order_confirmed",
		"data": map[string]interface{}{"OrderID": 42, "CustomerName": "sara", "Currency": "GBP", "Total": 239.97},
	})
	if code != http.StatusCreated {
		t.Fatalf("got %d, want 201", code)
	}
	var subject, body string
	if err := db.QueryRow(`SELECT subject, body FROM notifications`).Scan(&subject, &body); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if subject != "Order Confirmed - #42" {
		t.Errorf("subject %q", subject)
	}
	if body != "Hi sara, your order #42 has been confirmed. Total: GBP 239.97" {
		t.Errorf("body %q", body)
	}
}

func TestSameDedupeKeyIsOnlySentOnce(t *testing.T) {
	setupTestDB(t)
	payload := map[string]interface{}{
		"recipient": "sara@example.com", "template": "order_confirmed", "dedupe_key": "order_confirmed:42",
		"data": map[string]interface{}{"OrderID": 42},
	}
	code, _ := send(t, payload)
	if code != http.StatusCreated {
		t.Fatalf("first send: got %d, want 201", code)
	}
	code, resp := send(t, payload)
	if code != http.StatusOK || resp["status"] != "already_sent" {
		t.Fatalf("second send: got %d %v, want 200 already_sent", code, resp)
	}
	if n := countSent(t, "order_confirmed"); n != 1 {
		t.Errorf("%d notifications stored, want 1", n)
	}
}

func TestDifferentDedupeKeysAreBothSent(t *testing.T) {
	setupTestDB(t)
	for _, key := range []string{"order_confirmed:42", "order_confirmed:43"} {
		code, _ := send(t, map[string]interface{}{"recipient": "a@b.c", "template": "order_confirmed", "dedupe_key": key})
		if code != http.StatusCreated {
			t.Fatalf("%s: got %d, want 201", key, code)
		}
	}
	if n := countSent(t, "order_confirmed"); n != 2 {
		t.Errorf("%d notifications stored, want 2", n)
	}
}

func TestWithoutDedupeKeyEverySendIsStored(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < 2; i++ {
		code, _ := send(t, map[string]interface{}{"recipient": "a@b.c", "template": "low_stock_alert"})
		if code != http.StatusCreated {
			t.Fatalf("got %d, want 201", code)
		}
	}
	if n := countSent(t, "low_stock_alert"); n != 2 {
		t.Errorf("%d notifications stored, want 2", n)
	}
}

func TestConcurrentSendsWithOneKeyStoreOne(t *testing.T) {
	setupTestDB(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			send(t, map[string]interface{}{"recipient": "a@b.c", "template": "order_shipped", "dedupe_key": "order_shipped:7"})
		}()
	}
	wg.Wait()
	if n := countSent(t, "order_shipped"); n != 1 {
		t.Errorf("%d notifications stored, want 1", n)
	}
}

func TestUnknownTemplateStillSends(t *testing.T) {
	setupTestDB(t)
	code, _ := send(t, map[string]interface{}{"recipient": "a@b.c", "template": "no_such_template"})
	if code != http.StatusCreated {
		t.Fatalf("got %d, want 201 (unknown templates are sent as named)", code)
	}
}

func TestSendNeedsRecipientAndTemplate(t *testing.T) {
	code, _ := send(t, map[string]interface{}{"template": "order_confirmed"})
	if code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", code)
	}
}

// The cancellation email has a template, and it fills in the order's details.
func TestOrderCancelledTemplateIsSeededAndRenders(t *testing.T) {
	setupTestDB(t)
	// Start from a database that has never seen the template, as a fresh one would.
	if _, err := db.Exec(`DELETE FROM notification_templates WHERE id = 'order_cancelled'`); err != nil {
		t.Fatal(err)
	}
	migrate()
	code, _ := send(t, map[string]interface{}{
		"recipient": "sara@example.com", "template": "order_cancelled", "dedupe_key": "order_cancelled:42",
		"data": map[string]interface{}{"OrderID": 42, "CustomerName": "sara", "Currency": "GBP", "Total": "20.00"},
	})
	if code != http.StatusCreated {
		t.Fatalf("got %d, want 201", code)
	}
	var subject, body string
	if err := db.QueryRow(`SELECT subject, body FROM notifications WHERE template = 'order_cancelled'`).Scan(&subject, &body); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if subject != "Order Cancelled - #42" || !strings.Contains(body, "GBP 20.00") || !strings.Contains(body, "sara") {
		t.Errorf("rendered %q / %q", subject, body)
	}
}
