output "vpc_id" {
  value = module.vpc.vpc_id
}

output "sqs_queue_url" {
  description = "URL of the order-events queue, for SQS_QUEUE_URL in the Helm values"
  value       = module.sqs.queue_url
}

output "db_connection_secret_name" {
  description = "Database host/port secret, used as database.connectionSecret in the Helm values."
  value       = module.secrets_manager.db_connection_secret_name
}

output "db_credentials_secret_name" {
  description = "Database username/password secret, used as database.credentialsSecret in the Helm values."
  value       = module.secrets_manager.db_credentials_secret_name
}
