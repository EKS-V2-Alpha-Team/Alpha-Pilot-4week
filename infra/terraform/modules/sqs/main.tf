# Dead-letter queue configured it so that messages land here after 3 failed tries
resource "aws_sqs_queue" "order_events_dlq" {
  name = "${var.project_name}-${var.environment}-order-events-dlq"
  # 14 days (the maximum), so failed messages outlive the main queue's 4 days
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

# Main queue. order-service sends order events, the worker reads them
resource "aws_sqs_queue" "order_events" {
  name = "${var.project_name}-${var.environment}-order-events"

  visibility_timeout_seconds = 60
  # Long polling, matches the worker's WaitTimeSeconds of 20 in go code
  receive_wait_time_seconds = 20
  # 4 days (the AWS default)
  message_retention_seconds = 345600
  sqs_managed_sse_enabled   = true
}

# Redrive settings live in their own resources
# This also avoids a cycle, since each queue's policy points at the other queue.
resource "aws_sqs_queue_redrive_policy" "order_events" {
  queue_url = aws_sqs_queue.order_events.url

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.order_events_dlq.arn
    maxReceiveCount     = 3
  })
}

# Only the order-events queue may use this DLQ
resource "aws_sqs_queue_redrive_allow_policy" "order_events_dlq" {
  queue_url = aws_sqs_queue.order_events_dlq.url

  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.order_events.arn]
  })
}

# Fires when any message sits in the DLQ: an order event failed 3 times and its order is
# stuck until someone looks (runbook: docs/sqs/dlq-runbook.md). AWS recommends
# ApproximateNumberOfMessagesVisible for DLQs; NumberOfMessagesSent does not count messages
# moved there by the redrive policy. A quiet queue sends no data points, which counts as
# fine, not as an alarm.
resource "aws_cloudwatch_metric_alarm" "order_events_dlq_not_empty" {
  alarm_name          = "${var.project_name}-${var.environment}-order-events-dlq-not-empty"
  alarm_description   = "Order events failed 3 times and are waiting in the DLQ. Runbook: docs/sqs/dlq-runbook.md"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.order_events_dlq.name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = var.dlq_alarm_actions
  ok_actions          = var.dlq_alarm_actions
}
