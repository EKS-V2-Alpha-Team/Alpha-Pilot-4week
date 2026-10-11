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

output "dlq_alarm_name" {
  description = "Name of the CloudWatch alarm that fires when the DLQ is not empty"
  value       = aws_cloudwatch_metric_alarm.order_events_dlq_not_empty.alarm_name
}
