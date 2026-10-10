package main

// Retention: how long each table keeps its rows. Without this the outbox, the
// idempotency keys and the notification history only ever grow.
//
// The cut-off is worked out by Postgres (NOW() - an interval), never by the Go clock.
// Several of these columns are TIMESTAMP without a time zone, and handing the database
// a Go time makes it compare wall-clock readings, which is wrong whenever this process and
// the database are in different zones (the same bug that cancelled young orders).

import (
	"database/sql"
	"log"
	"time"
)

type retentionRule struct {
	table string
	// sql must delete the old rows and take the age in seconds as $1.
	sql string
	age time.Duration
}

var retentionRules = []retentionRule{
	{"order_events", `DELETE FROM order_events WHERE created_at < NOW() - make_interval(secs => $1)`, 90 * 24 * time.Hour},
	// Only rows the relay has delivered. An unsent row is an order event still waiting
	// to leave, however old it is, so it is never deleted here.
	{"order_outbox", `DELETE FROM order_outbox WHERE sent_at IS NOT NULL AND sent_at < NOW() - make_interval(secs => $1)`, 7 * 24 * time.Hour},
	// A retry after 24 hours is a new attempt, not a retry of the old one.
	{"idempotency_keys", `DELETE FROM idempotency_keys WHERE created_at < NOW() - make_interval(secs => $1)`, 24 * time.Hour},
	// As long as a message can wait in the DLQ.
	{"payment_idempotency_keys", `DELETE FROM payment_idempotency_keys WHERE created_at < NOW() - make_interval(secs => $1)`, 14 * 24 * time.Hour},
	{"notifications", `DELETE FROM notifications WHERE created_at < NOW() - make_interval(secs => $1)`, 90 * 24 * time.Hour},
}

// pruneOldRows applies every rule. A failing rule (for example a table its owning service
// has not created yet) is logged and the others still run.
func pruneOldRows(conn *sql.DB, rules []retentionRule) {
	for _, r := range rules {
		res, err := conn.Exec(r.sql, r.age.Seconds())
		if err != nil {
			log.Printf("retention: %s: %v", r.table, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("retention: deleted %d rows from %s older than %s", n, r.table, r.age)
		}
	}
}
