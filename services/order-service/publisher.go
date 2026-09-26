package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// publisher sends one already-encoded event to the message queue.
// The outbox relay depends on this interface, not on SQS directly,
// so tests can swap in a fake and prove behaviour without a real queue.
type publisher interface {
	Publish(ctx context.Context, body string) error
}

// sqsPublisher is the real implementation backed by Amazon SQS.
type sqsPublisher struct {
	client   *sqs.Client
	queueURL string
}

// newSQSPublisher builds an SQS client from the standard AWS environment:
// AWS_REGION, credentials (env vars locally, IRSA / Pod Identity on EKS) and,
// only for local development, AWS_ENDPOINT_URL pointing at Floci.
func newSQSPublisher(ctx context.Context, queueURL string) (*sqsPublisher, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &sqsPublisher{client: sqs.NewFromConfig(cfg), queueURL: queueURL}, nil
}

func (p *sqsPublisher) Publish(ctx context.Context, body string) error {
	_, err := p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.queueURL),
		MessageBody: aws.String(body),
	})
	if err != nil {
		return fmt.Errorf("sqs SendMessage: %w", err)
	}
	return nil
}
