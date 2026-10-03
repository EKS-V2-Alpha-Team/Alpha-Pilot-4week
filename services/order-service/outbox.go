package main

// Transactional outbox for order-service events.
//
// Problem it solves: saving an order (Postgres) and announcing it (SQS) are two
// separate systems. If the announcement is lost after the order is saved, nothing
// downstream ever reserves stock or takes payment, and the order is silently
// cancelled 30 minutes later by the scheduler.
//
// How it works:
//  1. Handlers write the event into order_outbox inside the SAME database
//     transaction as the order change (enqueueEvent). Both are saved or neither is.
//  2. A background relay (runOutboxRelay) reads unsent rows, sends them to SQS,
//     and marks them sent. If SQS is down, rows wait and are retried every tick.
//
// Guarantee: no event is lost. Not guaranteed: an event is sent only once — if the
// process dies after SQS accepts a message but before sent_at is saved, the row is
// sent again. Consumers must treat the envelope "id" as a de-duplication key.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// outboxBatchSize caps how many events one relay pass sends.
const outboxBatchSize = 10

type outboxEvent struct {
	id        int64
	eventType string
	payload   json.RawMessage
	createdAt time.Time
}

// enqueueEvent records an event in the outbox using the caller's transaction.
func enqueueEvent(tx *sql.Tx, eventType string, payload map[string]interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", eventType, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO order_outbox (event_type, payload) VALUES ($1, $2)`,
		eventType, data,
	); err != nil {
		return fmt.Errorf("insert %s into outbox: %w", eventType, err)
	}
	return nil
}

// envelope builds the message body. type/payload/timestamp keep the exact shape the
// worker already parses; "id" is new and unique per event, for consumer de-duplication.
func (e outboxEvent) envelope() (string, error) {
	b, err := json.Marshal(map[string]interface{}{
		"id":        fmt.Sprintf("order-service-%d", e.id),
		"type":      e.eventType,
		"payload":   e.payload,
		"timestamp": e.createdAt.UTC().Format(time.RFC3339),
	})
	return string(b), err
}

// runOutboxRelay calls relayOutboxOnce every interval until ctx is cancelled.
func runOutboxRelay(ctx context.Context, pub publisher, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sent, err := relayOutboxOnce(ctx, pub)
			if err != nil {
				log.Printf("outbox relay: %v", err)
			}
			if sent > 0 {
				log.Printf("outbox relay: sent %d event(s)", sent)
			}
		}
	}
}

// relayOutboxOnce sends up to one batch of unsent events and returns how many were sent.
func relayOutboxOnce(ctx context.Context, pub publisher) (int, error) {
	// Database bookkeeping must finish even during shutdown, otherwise a message that
	// SQS already accepted would lose its sent_at and be sent again on restart.
	dbCtx := context.WithoutCancel(ctx)

	tx, err := db.BeginTx(dbCtx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	// FOR UPDATE SKIP LOCKED: with several order-service replicas, each relay claims
	// different rows instead of two replicas sending the same event at the same time.
	rows, err := tx.QueryContext(dbCtx,
		`SELECT id, event_type, payload, created_at
		   FROM order_outbox
		  WHERE sent_at IS NULL
		  ORDER BY id
		  LIMIT $1
		  FOR UPDATE SKIP LOCKED`, outboxBatchSize)
	if err != nil {
		return 0, fmt.Errorf("select pending: %w", err)
	}
	var pending []outboxEvent
	for rows.Next() {
		var e outboxEvent
		if err := rows.Scan(&e.id, &e.eventType, &e.payload, &e.createdAt); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan pending: %w", err)
		}
		pending = append(pending, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read pending: %w", err)
	}

	sent := 0
	for _, e := range pending {
		body, err := e.envelope()
		if err != nil {
			return sent, fmt.Errorf("encode event %d: %w", e.id, err)
		}

		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		pubErr := pub.Publish(sendCtx, body)
		cancel()

		if pubErr != nil {
			// Keep the row unsent and record why. Stop this pass: a failure almost always
			// means the queue is unreachable, so the rest of the batch would fail too.
			if _, err := tx.ExecContext(dbCtx,
				`UPDATE order_outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
				e.id, pubErr.Error()); err != nil {
				return sent, fmt.Errorf("record failure for event %d: %w", e.id, err)
			}
			if err := tx.Commit(); err != nil {
				return sent, fmt.Errorf("commit: %w", err)
			}
			return sent, fmt.Errorf("publish event %d: %w", e.id, pubErr)
		}

		if _, err := tx.ExecContext(dbCtx,
			`UPDATE order_outbox SET sent_at = NOW(), attempts = attempts + 1, last_error = NULL WHERE id = $1`,
			e.id); err != nil {
			return sent, fmt.Errorf("mark event %d sent: %w", e.id, err)
		}
		sent++
	}

	if err := tx.Commit(); err != nil {
		return sent, fmt.Errorf("commit: %w", err)
	}
	return sent, nil
}
