package main

// Tests for the scheduler's jobs that work on other services' tables: giving back expired
// stock holds, and pruning old rows. Each table is created with the same definition its
// owning service uses, so these tests also run on a database those services' own tests used.

import (
	"sync"
	"testing"
)

func setupHousekeepingDB(t *testing.T) {
	t.Helper()
	setupTestDB(t) // connects, creates and empties orders
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id VARCHAR(50) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			sku VARCHAR(100) UNIQUE NOT NULL,
			price DECIMAL(10,2) NOT NULL,
			stock INTEGER NOT NULL DEFAULT 0,
			reserved INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS reservations (
			id SERIAL PRIMARY KEY,
			order_id INTEGER NOT NULL,
			product_id VARCHAR(50) NOT NULL REFERENCES products(id),
			quantity INTEGER NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_reservations_active_order_product
			ON reservations(order_id, product_id) WHERE status = 'active'`,
		`CREATE TABLE IF NOT EXISTS order_events (
			id SERIAL PRIMARY KEY,
			order_id INTEGER NOT NULL REFERENCES orders(id),
			event_type VARCHAR(50) NOT NULL,
			old_status VARCHAR(20),
			new_status VARCHAR(20),
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS order_outbox (
			id BIGSERIAL PRIMARY KEY,
			event_type VARCHAR(100) NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			sent_at TIMESTAMPTZ,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			customer_id VARCHAR(255) NOT NULL,
			key VARCHAR(255) NOT NULL,
			request_hash CHAR(64) NOT NULL,
			order_id INTEGER REFERENCES orders(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (customer_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			id SERIAL PRIMARY KEY,
			recipient VARCHAR(255) NOT NULL,
			channel VARCHAR(20) NOT NULL,
			template VARCHAR(100) NOT NULL,
			subject TEXT,
			body TEXT NOT NULL,
			metadata JSONB,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			sent_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
	}
	for _, q := range ddl {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("create table: %v\n%s", err, q)
		}
	}
	if _, err := db.Exec(`TRUNCATE order_outbox, notifications, reservations, products RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func countRows(t *testing.T, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// addProduct creates a product with the given stock and reserved counts.
func addProduct(t *testing.T, id string, stock, reserved int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO products (id, name, sku, price, stock, reserved) VALUES ($1, $1, $1, 5, $2, $3)`, id, stock, reserved); err != nil {
		t.Fatalf("add product: %v", err)
	}
}

// addReservation holds qty of a product for an order; minutesLeft < 0 means already expired.
func addReservation(t *testing.T, orderID int, product string, qty, minutesLeft int) int {
	t.Helper()
	var id int
	err := db.QueryRow(
		`INSERT INTO reservations (order_id, product_id, quantity, status, expires_at)
		 VALUES ($1, $2, $3, 'active', NOW() + make_interval(mins => $4)) RETURNING id`,
		orderID, product, qty, minutesLeft,
	).Scan(&id)
	if err != nil {
		t.Fatalf("add reservation: %v", err)
	}
	return id
}

func productCounts(t *testing.T, id string) (stock, reserved int) {
	t.Helper()
	if err := db.QueryRow(`SELECT stock, reserved FROM products WHERE id = $1`, id).Scan(&stock, &reserved); err != nil {
		t.Fatalf("product counts: %v", err)
	}
	return
}

func reservationStatus(t *testing.T, id int) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM reservations WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("reservation status: %v", err)
	}
	return s
}

func TestExpiredHoldGivesStockBackOnce(t *testing.T) {
	setupHousekeepingDB(t)
	addProduct(t, "p1", 20, 8)
	old := addReservation(t, 1, "p1", 3, -5)
	live := addReservation(t, 2, "p1", 5, 10)

	expireReservations()
	expireReservations() // a second run must not give it back again

	if s := reservationStatus(t, old); s != "expired" {
		t.Errorf("old hold: %s, want expired", s)
	}
	if s := reservationStatus(t, live); s != "active" {
		t.Errorf("live hold: %s, want active", s)
	}
	if _, reserved := productCounts(t, "p1"); reserved != 5 {
		t.Errorf("reserved: got %d, want 5 (8 minus the expired 3)", reserved)
	}
}

// The order was paid (committed) in the instant the scheduler had already picked its hold
// as expired. The commit already took the stock off; the scheduler must not give it back.
func TestHoldCommittedJustBeforeExpiryIsLeftAlone(t *testing.T) {
	setupHousekeepingDB(t)
	addProduct(t, "p1", 20, 10)
	id := addReservation(t, 1, "p1", 3, -1)
	// what inventory-service's /commit does:
	if _, err := db.Exec(`UPDATE reservations SET status = 'committed' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE products SET stock = stock - 3, reserved = reserved - 3 WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}

	expired, err := expireReservation(id) // the scheduler acting on the id it had already read
	if err != nil || expired {
		t.Fatalf("expireReservation = %v, %v; want false, nil", expired, err)
	}
	stock, reserved := productCounts(t, "p1")
	if stock != 17 || reserved != 7 {
		t.Errorf("stock %d reserved %d, want 17 and 7", stock, reserved)
	}
	if s := reservationStatus(t, id); s != "committed" {
		t.Errorf("status %s, want committed", s)
	}
}

// Commit and expiry at the same moment, many times. Whichever wins, the numbers must add up:
// committed -> stock down 3 and reserved down 3; expired -> stock unchanged, reserved down 3.
// Giving the stock back on top of a commit would show as reserved down 6.
func TestCommitAndExpiryRacingNeverGiveStockBackTwice(t *testing.T) {
	setupHousekeepingDB(t)
	db.SetMaxOpenConns(6)
	db.SetMaxIdleConns(6)
	var warm sync.WaitGroup
	for i := 0; i < 6; i++ {
		warm.Add(1)
		go func() { defer warm.Done(); db.Exec(`SELECT pg_sleep(0.05)`) }()
	}
	warm.Wait()

	for run := 0; run < 40; run++ {
		db.Exec(`TRUNCATE reservations, products RESTART IDENTITY CASCADE`)
		addProduct(t, "p1", 20, 10)
		id := addReservation(t, 1, "p1", 3, -1)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			expireReservation(id)
		}()
		go func() {
			defer wg.Done()
			<-start
			commitLikeInventory(1)
		}()
		close(start)
		wg.Wait()

		stock, reserved := productCounts(t, "p1")
		switch s := reservationStatus(t, id); s {
		case "committed":
			if stock != 17 || reserved != 7 {
				t.Fatalf("run %d: committed but stock %d reserved %d, want 17 and 7", run, stock, reserved)
			}
		case "expired":
			if stock != 20 || reserved != 7 {
				t.Fatalf("run %d: expired but stock %d reserved %d, want 20 and 7", run, stock, reserved)
			}
		default:
			t.Fatalf("run %d: status %s", run, s)
		}
	}
}

// commitLikeInventory is inventory-service's /commit for one order: claim, then move the stock.
func commitLikeInventory(orderID int) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	var product string
	var qty int
	err = tx.QueryRow(`UPDATE reservations SET status = 'committed' WHERE order_id = $1 AND status = 'active' RETURNING product_id, quantity`, orderID).Scan(&product, &qty)
	if err != nil {
		return
	}
	tx.Exec(`UPDATE products SET stock = stock - $1, reserved = reserved - $1 WHERE id = $2`, qty, product)
	tx.Commit()
}

func TestOneBadReservationDoesNotStopTheOthers(t *testing.T) {
	setupHousekeepingDB(t)
	addProduct(t, "p1", 20, 6)
	addProduct(t, "p2", 20, 6)
	a := addReservation(t, 1, "p1", 3, -5)
	b := addReservation(t, 2, "p2", 3, -5)
	// make the first one fail: its stock update would break a constraint
	if _, err := db.Exec(`ALTER TABLE products ADD CONSTRAINT p1_reserved_fixed CHECK (id <> 'p1' OR reserved = 6) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`ALTER TABLE products DROP CONSTRAINT IF EXISTS p1_reserved_fixed`) })

	expireReservations()

	if s := reservationStatus(t, a); s != "active" {
		t.Errorf("failed reservation: %s, want active (rolled back)", s)
	}
	if s := reservationStatus(t, b); s != "expired" {
		t.Errorf("other reservation: %s, want expired", s)
	}
	if _, reserved := productCounts(t, "p1"); reserved != 6 {
		t.Errorf("p1 reserved %d, want 6 (nothing half done)", reserved)
	}
}

// --- retention ---

func TestRetentionDeletesOldRowsAndKeepsRecentOnes(t *testing.T) {
	setupHousekeepingDB(t)
	id := insertOrder(t, "pending", 1)
	mustExec(t, `INSERT INTO order_events (order_id, event_type, created_at) VALUES ($1, 'old', NOW() - interval '91 days'), ($1, 'new', NOW() - interval '89 days')`, id)
	mustExec(t, `INSERT INTO order_outbox (event_type, payload, sent_at) VALUES
		('sent-old', '{}', NOW() - interval '8 days'), ('sent-new', '{}', NOW() - interval '6 days'),
		('unsent-old', '{}', NULL)`)
	mustExec(t, `UPDATE order_outbox SET created_at = NOW() - interval '30 days' WHERE event_type = 'unsent-old'`)
	mustExec(t, `INSERT INTO idempotency_keys (customer_id, key, request_hash, order_id, created_at) VALUES
		('a', 'old', 'h', $1, NOW() - interval '25 hours'), ('a', 'new', 'h', $1, NOW() - interval '23 hours')`, id)
	mustExec(t, `INSERT INTO notifications (recipient, channel, template, body, created_at) VALUES
		('a', 'email', 't', 'b', NOW() - interval '91 days'), ('a', 'email', 't', 'b', NOW() - interval '89 days')`)

	cleanupOldEvents()

	checks := []struct {
		what  string
		query string
		want  int
	}{
		{"order_events kept", `SELECT count(*) FROM order_events`, 1},
		{"sent outbox kept", `SELECT count(*) FROM order_outbox WHERE event_type = 'sent-new'`, 1},
		{"old sent outbox deleted", `SELECT count(*) FROM order_outbox WHERE event_type = 'sent-old'`, 0},
		{"UNSENT outbox row is never deleted", `SELECT count(*) FROM order_outbox WHERE event_type = 'unsent-old'`, 1},
		{"idempotency keys kept", `SELECT count(*) FROM idempotency_keys WHERE key = 'new'`, 1},
		{"old idempotency key deleted", `SELECT count(*) FROM idempotency_keys WHERE key = 'old'`, 0},
		{"notifications kept", `SELECT count(*) FROM notifications`, 1},
	}
	for _, c := range checks {
		if got := countRows(t, c.query); got != c.want {
			t.Errorf("%s: got %d, want %d", c.what, got, c.want)
		}
	}
}

// A table that does not exist yet (its service has not started) is logged and skipped.
func TestRetentionCarriesOnWhenOneTableIsMissing(t *testing.T) {
	setupHousekeepingDB(t)
	mustExec(t, `INSERT INTO notifications (recipient, channel, template, body, created_at) VALUES ('a', 'email', 't', 'b', NOW() - interval '91 days')`)
	rules := append([]retentionRule{{"missing", `DELETE FROM no_such_table WHERE $1 > 0`, 1}}, retentionRules...)

	pruneOldRows(db, rules)

	if n := countRows(t, `SELECT count(*) FROM notifications`); n != 0 {
		t.Errorf("notifications: %d left, want 0 (the rule after the failing one must still run)", n)
	}
}

func mustExec(t *testing.T, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}
