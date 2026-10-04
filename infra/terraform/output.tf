output "vpc_id" {
  value = module.vpc.vpc_id
}

output "sqs_queue_url" {
  description = "URL of the order-events queue, for SQS_QUEUE_URL in the Helm values"
  value       = module.sqs.queue_url
}

output "db_address" {
  description = "RDS hostname for DATABASE_URL in Helm values."
  value       = module.rds.db_address
}

output "db_credentials_secret_name" {
  description = "Database credentials secret"
  value       = module.secrets_manager.db_credentials_secret_name
}

