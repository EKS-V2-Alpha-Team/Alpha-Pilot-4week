#!/usr/bin/env bash
set -euo pipefail

aws sqs create-queue --queue-name order-events-dlq

DLQ_ARN=$(aws sqs get-queue-attributes \
  --queue-url http://floci:4566/000000000000/order-events-dlq \
  --attribute-names QueueArn \
  --query 'Attributes.QueueArn' \
  --output text)

aws sqs create-queue --queue-name order-events \
  --attributes "{\"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"${DLQ_ARN}\\\",\\\"maxReceiveCount\\\":\\\"3\\\"}\"}"