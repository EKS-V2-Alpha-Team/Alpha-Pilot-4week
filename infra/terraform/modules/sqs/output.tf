output "queue_url" {
  description = "URL of the order-events queue. The apps read it as SQS_QUEUE_URL"
  value       = aws_sqs_queue.order_events.url
}

output "queue_arn" {
  description = "ARN of the order-events queue. Used for IAM permissions"
  value       = aws_sqs_queue.order_events.arn
}

output "dlq_arn" {
  description = "ARN of the order-events dead-letter queue"
  value       = aws_sqs_queue.order_events_dlq.arn
}
