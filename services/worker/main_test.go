package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeServices stands in for inventory, payment, order, shipping and notification
// service. It records every call ("POST /reserve", "PUT /status confirmed", ...) and
// answers with the status codes the test sets.
type fakeServices struct {
	mu          sync.Mutex
	calls       []string
	bodies      map[string]map[string]interface{} // call -> last JSON body sent
	codes       map[string]int                    // path -> status code, default 200
	orderStatus string                            // what GET /42 reports, default "pending"
}

func newFakeServices(t *testing.T, codes map[string]int) (*fakeServices, map[string]string) {
	t.Helper()
	return newFakeServicesWithStatus(t, codes, "pending")
}

func newFakeServicesWithStatus(t *testing.T, codes map[string]int, orderStatus string) (*fakeServices, map[string]string) {
	t.Helper()
	f := &fakeServices{codes: codes, orderStatus: orderStatus, bodies: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + r.URL.Path
		var body map[string]interface{}
		if r.Method != http.MethodGet {
			json.NewDecoder(r.Body).Decode(&body)
		}
		// Calls that differ by what they ask for are recorded with it.
		switch {
		case r.URL.Path == "/status":
			call += " " + body["new_status"].(string)
		case r.URL.Path == "/send":
			call += " " + body["template"].(string)
		case strings.HasPrefix(r.URL.Path, "/shipments/by-order/"):
			call += " " + body["status"].(string)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		f.bodies[call] = body
		code, ok := f.codes[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		if code != http.StatusOK {
			return
		}
		switch {
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": f.orderStatus, "customer_id": "sara@example.com", "total": 239.97, "currency": "GBP",
			})
		case strings.HasPrefix(r.URL.Path, "/shipments/by-order/"):
			json.NewEncoder(w).Encode(map[string]interface{}{"tracking_number": "RM123"})
		}
	}))
	t.Cleanup(srv.Close)
	return f, map[string]string{
		"inventory": srv.URL, "payment": srv.URL, "order": srv.URL, "shipping": srv.URL, "notification": srv.URL,
	}
}

func orderCreated(t *testing.T) Event {
	t.Helper()
	payload, _ := json.Marshal(map[string]interface{}{
		"order_id": 42, "customer_id": "sara@example.com", "total": 20.0, "currency": "GBP",
		"items": []map[string]interface{}{{"product_id": "mug-1", "quantity": 2, "price": 10.0}},
	})
	return Event{ID: "order-service-1", Type: "order.created", Payload: payload}
}

func run(t *testing.T, codes map[string]int, event Event) ([]string, error) {
	t.Helper()
	f, services := newFakeServices(t, codes)
	err := handleEvent(&http.Client{Timeout: 2 * time.Second}, services, event)
	return f.calls, err
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls:\n got %q\nwant %q", got, want)
	}
}

func TestNewOrderIsReservedChargedAndConfirmed(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 201}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil (message deleted)", err)
	}
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "POST /charge", "PUT /status confirmed", "POST /commit"})
}

func TestRepeatedOrderCreatedStillConfirms(t *testing.T) {
	// Second delivery of the same message: inventory and payment answer "already done" (200).
	calls, err := run(t, map[string]int{"/reserve": 200, "/charge": 200}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "POST /charge", "PUT /status confirmed", "POST /commit"})
}

func TestOutOfStockCancelsWithoutCharging(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 409}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil (final answer, message deleted)", err)
	}
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "PUT /status cancelled"})
}

func TestDeclinedCardReleasesStockAndCancels(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 402}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil (final answer, message deleted)", err)
	}
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "POST /charge", "POST /release", "PUT /status cancelled", "POST /send payment_failed"})
}

func TestFailedPaymentEmailDoesNotRetryTheOrder(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 402, "/send": 500}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil: the order is already cancelled, only the email failed", err)
	}
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "POST /charge", "POST /release", "PUT /status cancelled", "POST /send payment_failed"})
}

func TestPaymentOutageKeepsMessageForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 500}, orderCreated(t))
	if err == nil {
		t.Fatal("got nil, want an error so the message stays on the queue")
	}
	// Stock stays held and the order stays pending; the retry picks up from here.
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "POST /charge"})
}

func TestInventoryUnreachableKeepsMessageForRetry(t *testing.T) {
	services := map[string]string{"inventory": "http://127.0.0.1:1", "payment": "http://127.0.0.1:1", "order": "http://127.0.0.1:1"}
	if err := handleEvent(&http.Client{Timeout: time.Second}, services, orderCreated(t)); err == nil {
		t.Fatal("got nil, want an error so the message stays on the queue")
	}
}

func TestCommitFailureKeepsMessageForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 201, "/commit": 500}, orderCreated(t))
	if err == nil {
		t.Fatal("got nil, want an error so the message stays on the queue")
	}
	assertCalls(t, calls, []string{"GET /42", "POST /reserve", "POST /charge", "PUT /status confirmed", "POST /commit"})
}

func TestEventsWithoutHandlerAreSkipped(t *testing.T) {
	calls, err := run(t, nil, Event{ID: "order-service-2", Type: "shipment.created", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("got error %v, want nil (deleted, not sent to the DLQ)", err)
	}
	assertCalls(t, calls, nil)
}

// TestQueueReceiveAndDelete runs against a real SQS API. Skipped unless AWS_ENDPOINT_URL
// is set, e.g. against Floci: AWS_ENDPOINT_URL=http://localhost:4566 AWS_REGION=us-east-1
// AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test go test ./...
func TestQueueReceiveAndDelete(t *testing.T) {
	if os.Getenv("AWS_ENDPOINT_URL") == "" {
		t.Skip("AWS_ENDPOINT_URL not set; skipping SQS test")
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	client := sqs.NewFromConfig(cfg)
	created, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("worker-test-" + time.Now().Format("150405"))})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	t.Cleanup(func() { client.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: created.QueueUrl}) })
	q := &sqsQueue{client: client, url: aws.ToString(created.QueueUrl)}

	if _, err := client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: created.QueueUrl, MessageBody: aws.String(`{"id":"x","type":"t"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs, err := q.receive(ctx)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("receive: got %d messages, err %v; want 1", len(msgs), err)
	}
	if err := q.delete(ctx, msgs[0].ReceiptHandle); err != nil {
		t.Fatalf("delete: %v", err)
	}
	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: created.QueueUrl, AttributeNames: []types.QueueAttributeName{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible"},
	})
	if err != nil {
		t.Fatalf("attributes: %v", err)
	}
	if attrs.Attributes["ApproximateNumberOfMessages"] != "0" || attrs.Attributes["ApproximateNumberOfMessagesNotVisible"] != "0" {
		t.Errorf("queue not empty after delete: %v", attrs.Attributes)
	}
}

func runWithStatus(t *testing.T, codes map[string]int, orderStatus string) ([]string, error) {
	t.Helper()
	f, services := newFakeServicesWithStatus(t, codes, orderStatus)
	err := handleEvent(&http.Client{Timeout: 2 * time.Second}, services, orderCreated(t))
	return f.calls, err
}

func TestRedeliveryOfConfirmedOrderOnlyCommitsStock(t *testing.T) {
	// The first run confirmed the order but its commit failed; this is the retry.
	calls, err := runWithStatus(t, nil, "confirmed")
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, calls, []string{"GET /42", "POST /commit"})
}

func TestRedeliveryOfCancelledOrderNeverChargesAgain(t *testing.T) {
	// The first run cancelled the order (declined card) but the delete failed.
	calls, err := runWithStatus(t, nil, "cancelled")
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	// The cancel came in while this message was in flight: undo everything it took.
	assertCalls(t, calls, []string{"GET /42", "POST /release", "POST /restock", "POST /refund"})
}

func TestOrderServiceDownKeepsMessageForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/42": 503}, orderCreated(t))
	if err == nil {
		t.Fatal("got nil, want an error so the message stays on the queue")
	}
	assertCalls(t, calls, []string{"GET /42"})
}

func TestReleaseFailureOnCancelledOrderKeepsMessageForRetry(t *testing.T) {
	calls, err := runWithStatus(t, map[string]int{"/release": 500}, "cancelled")
	if err == nil {
		t.Fatal("got nil, want an error so the message stays on the queue")
	}
	assertCalls(t, calls, []string{"GET /42", "POST /release"})
}

func statusChanged(t *testing.T, oldStatus, newStatus string) Event {
	t.Helper()
	payload, _ := json.Marshal(map[string]interface{}{"order_id": 42, "old_status": oldStatus, "new_status": newStatus})
	return Event{ID: "order-service-9", Type: "order.status_changed", Payload: payload}
}

// The case behind the live bug: a paid, confirmed order is cancelled. Stock goes back
// and the charge is refunded.
func TestCancelledOrderGetsStockAndMoneyBack(t *testing.T) {
	calls, err := run(t, nil, statusChanged(t, "confirmed", "cancelled"))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, calls, []string{"POST /release", "POST /restock", "POST /refund", "PUT /shipments/by-order/42 cancelled"})
}

// Order that never got charged: payment-service answers 404 and that is fine.
func TestCancelledOrderThatWasNeverChargedIsStillFine(t *testing.T) {
	calls, err := run(t, map[string]int{"/refund": 404, "/shipments/by-order/42": 404}, statusChanged(t, "pending", "cancelled"))
	if err != nil {
		t.Fatalf("got error %v, want nil (nothing to refund, no shipment to cancel)", err)
	}
	assertCalls(t, calls, []string{"POST /release", "POST /restock", "POST /refund", "PUT /shipments/by-order/42 cancelled"})
}

func runStatus(t *testing.T, codes map[string]int, orderStatus string, event Event) (*fakeServices, error) {
	t.Helper()
	f, services := newFakeServicesWithStatus(t, codes, orderStatus)
	err := handleEvent(&http.Client{Timeout: 2 * time.Second}, services, event)
	return f, err
}

func TestConfirmedOrderEmailsTheCustomer(t *testing.T) {
	f, err := runStatus(t, nil, "confirmed", statusChanged(t, "pending", "confirmed"))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /send order_confirmed"})
	sent := f.bodies["POST /send order_confirmed"]
	if sent["recipient"] != "sara@example.com" || sent["dedupe_key"] != "order_confirmed:42" {
		t.Errorf("email addressed wrongly: %v", sent)
	}
	data := sent["data"].(map[string]interface{})
	if data["Total"] != "239.97" || data["CustomerName"] != "sara" || data["Currency"] != "GBP" || data["OrderID"] != float64(42) {
		t.Errorf("email data wrong: %v", data)
	}
}

func TestProcessingOrderGetsAShipmentWithPlaceholderAddress(t *testing.T) {
	f, err := runStatus(t, map[string]int{"/shipments": 201}, "processing", statusChanged(t, "confirmed", "processing"))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /shipments"})
	sent := f.bodies["POST /shipments"]
	if sent["order_id"] != float64(42) || sent["recipient_name"] != "sara@example.com" {
		t.Errorf("shipment request wrong: %v", sent)
	}
	if addr, _ := sent["address_line1"].(string); !strings.HasPrefix(addr, "PLACEHOLDER") {
		t.Errorf("address_line1 %q, want the placeholder", addr)
	}
}

func TestShippedOrderMovesShipmentAndEmailsTrackingNumber(t *testing.T) {
	f, err := runStatus(t, nil, "shipped", statusChanged(t, "processing", "shipped"))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /shipments", "PUT /shipments/by-order/42 in_transit", "POST /send order_shipped"})
	data := f.bodies["POST /send order_shipped"]["data"].(map[string]interface{})
	if data["TrackingNumber"] != "RM123" {
		t.Errorf("tracking number %v, want RM123 from the shipment", data["TrackingNumber"])
	}
}

func TestDeliveredOrderMovesShipmentAndEmailsTheCustomer(t *testing.T) {
	f, err := runStatus(t, nil, "delivered", statusChanged(t, "shipped", "delivered"))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /shipments", "PUT /shipments/by-order/42 delivered", "POST /send order_delivered"})
}

// A late event for an order that was cancelled in the meantime must not email or ship.
func TestLateStatusEventsForCancelledOrderDoNothing(t *testing.T) {
	for _, next := range []string{"confirmed", "processing", "shipped", "delivered"} {
		f, err := runStatus(t, nil, "cancelled", statusChanged(t, "pending", next))
		if err != nil {
			t.Fatalf("%s: got error %v, want nil", next, err)
		}
		assertCalls(t, f.calls, []string{"GET /42"})
	}
}

func TestShippingOutageKeepsStatusEventForRetry(t *testing.T) {
	f, err := runStatus(t, map[string]int{"/shipments": 500}, "processing", statusChanged(t, "confirmed", "processing"))
	if err == nil {
		t.Fatal("got nil, want an error so the shipment is not forgotten")
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /shipments"})
}

func TestNotificationOutageKeepsConfirmedEventForRetry(t *testing.T) {
	f, err := runStatus(t, map[string]int{"/send": 503}, "confirmed", statusChanged(t, "pending", "confirmed"))
	if err == nil {
		t.Fatal("got nil, want an error so the email is not forgotten")
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /send order_confirmed"})
}

// The shipment update is done before the email, so a retry after an email failure just
// repeats a shipment update that changes nothing.
func TestShippedEmailFailureKeepsEventForRetry(t *testing.T) {
	f, err := runStatus(t, map[string]int{"/send": 500}, "shipped", statusChanged(t, "processing", "shipped"))
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	assertCalls(t, f.calls, []string{"GET /42", "POST /shipments", "PUT /shipments/by-order/42 in_transit", "POST /send order_shipped"})
}

func TestCancelShipmentFailureKeepsCancelEventForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/shipments/by-order/42": 500}, statusChanged(t, "processing", "cancelled"))
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	assertCalls(t, calls, []string{"POST /release", "POST /restock", "POST /refund", "PUT /shipments/by-order/42 cancelled"})
}

func TestUnknownStatusIsSkipped(t *testing.T) {
	calls, err := run(t, nil, statusChanged(t, "pending", "teleported"))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, calls, nil)
}

func TestRefundFailureKeepsCancelEventForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/refund": 500}, statusChanged(t, "confirmed", "cancelled"))
	if err == nil {
		t.Fatal("got nil, want an error so the money is not forgotten")
	}
	assertCalls(t, calls, []string{"POST /release", "POST /restock", "POST /refund"})
}

func TestRestockFailureKeepsCancelEventForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/restock": 500}, statusChanged(t, "confirmed", "cancelled"))
	if err == nil {
		t.Fatal("got nil, want an error so the stock is not forgotten")
	}
	// Stops before the refund: the retry redoes all three, and each is safe to repeat.
	assertCalls(t, calls, []string{"POST /release", "POST /restock"})
}

func TestBadStatusChangedPayloadKeepsMessageForRetry(t *testing.T) {
	_, err := run(t, nil, Event{ID: "x", Type: "order.status_changed", Payload: json.RawMessage(`not json`)})
	if err == nil {
		t.Fatal("got nil, want an error")
	}
}
