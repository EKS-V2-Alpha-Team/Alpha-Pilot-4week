output "vpc_id" {
  value = module.vpc.vpc_id
}

output "sqs_queue_url" {
  description = "URL of the order-events queue, for SQS_QUEUE_URL in the Helm values"
  value       = module.sqs.queue_url
}
