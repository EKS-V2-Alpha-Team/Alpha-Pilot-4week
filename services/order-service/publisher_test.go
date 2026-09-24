package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// TestSQSPublisherDeliversMessage talks to a real SQS API (Floci locally).
// It is skipped unless AWS_ENDPOINT_URL is set, so it never runs against
// real AWS by accident and never fails in a pipeline that has no emulator.
func TestSQSPublisherDeliversMessage(t *testing.T) {
	if os.Getenv("AWS_ENDPOINT_URL") == "" {
		t.Skip("AWS_ENDPOINT_URL not set; skipping SQS integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build a publisher first so the test uses exactly the same client setup as the service.
	p, err := newSQSPublisher(ctx, "")
	if err != nil {
		t.Fatalf("newSQSPublisher: %v", err)
	}

	// A throwaway queue per run, deleted at the end, so the test never touches order-events.
	name := "order-service-publisher-test-" + time.Now().Format("150405")
	q, err := p.client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	defer p.client.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: q.QueueUrl})
	p.queueURL = *q.QueueUrl

	body := `{"id":"order-outbox-1","type":"order.created","payload":{"order_id":1}}`
	if err := p.Publish(ctx, body); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	out, err := p.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:        q.QueueUrl,
		WaitTimeSeconds: 5,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(out.Messages) != 1 || *out.Messages[0].Body != body {
		t.Fatalf("expected exactly the published body back, got %+v", out.Messages)
	}
}

// TestSQSPublisherReturnsErrorForMissingQueue proves a wrong queue URL is
// reported as an error instead of being silently dropped.
func TestSQSPublisherReturnsErrorForMissingQueue(t *testing.T) {
	if os.Getenv("AWS_ENDPOINT_URL") == "" {
		t.Skip("AWS_ENDPOINT_URL not set; skipping SQS integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := newSQSPublisher(ctx, os.Getenv("AWS_ENDPOINT_URL")+"/000000000000/does-not-exist")
	if err != nil {
		t.Fatalf("newSQSPublisher: %v", err)
	}
	if err := p.Publish(ctx, "x"); err == nil {
		t.Fatal("expected an error publishing to a queue that does not exist")
	}
}
