package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeServices stands in for inventory, payment and order-service. It records every
// call ("POST /reserve", "PUT /status confirmed", ...) and answers with the status
// codes the test sets.
type fakeServices struct {
	mu    sync.Mutex
	calls []string
	codes map[string]int // path -> status code, default 200
}

func newFakeServices(t *testing.T, codes map[string]int) (*fakeServices, map[string]string) {
	t.Helper()
	f := &fakeServices{codes: codes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + r.URL.Path
		if r.URL.Path == "/status" {
			var body struct {
				NewStatus string `json:"new_status"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			call += " " + body.NewStatus
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		code, ok := f.codes[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			code = http.StatusOK
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return f, map[string]string{"inventory": srv.URL, "payment": srv.URL, "order": srv.URL}
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
	assertCalls(t, calls, []string{"POST /reserve", "POST /charge", "PUT /status confirmed", "POST /commit"})
}

func TestRepeatedOrderCreatedStillConfirms(t *testing.T) {
	// Second delivery of the same message: inventory and payment answer "already done" (200).
	calls, err := run(t, map[string]int{"/reserve": 200, "/charge": 200}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	assertCalls(t, calls, []string{"POST /reserve", "POST /charge", "PUT /status confirmed", "POST /commit"})
}

func TestOutOfStockCancelsWithoutCharging(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 409}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil (final answer, message deleted)", err)
	}
	assertCalls(t, calls, []string{"POST /reserve", "PUT /status cancelled"})
}

func TestDeclinedCardReleasesStockAndCancels(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 402}, orderCreated(t))
	if err != nil {
		t.Fatalf("got error %v, want nil (final answer, message deleted)", err)
	}
	assertCalls(t, calls, []string{"POST /reserve", "POST /charge", "POST /release", "PUT /status cancelled"})
}

func TestPaymentOutageKeepsMessageForRetry(t *testing.T) {
	calls, err := run(t, map[string]int{"/reserve": 201, "/charge": 500}, orderCreated(t))
	if err == nil {
		t.Fatal("got nil, want an error so the message stays on the queue")
	}
	// Stock stays held and the order stays pending; the retry picks up from here.
	assertCalls(t, calls, []string{"POST /reserve", "POST /charge"})
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
	assertCalls(t, calls, []string{"POST /reserve", "POST /charge", "PUT /status confirmed", "POST /commit"})
}

func TestEventsWithoutHandlerAreSkipped(t *testing.T) {
	calls, err := run(t, nil, Event{ID: "order-service-2", Type: "order.status_changed", Payload: json.RawMessage(`{}`)})
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
