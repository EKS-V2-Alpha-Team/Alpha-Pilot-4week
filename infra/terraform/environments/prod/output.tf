output "vpc_id" {
  value = module.vpc.vpc_id
}

output "sqs_queue_url" {
  description = "URL of the order-events queue, for SQS_QUEUE_URL in the Helm values"
  value       = module.sqs.queue_url
}

output "db_address" {
  description = "Hostname for application connections"
  value       = module.rds.db_address
}

output "db_connection_secret_name" {
  description = "Name of the secret holding the RDS host and port, used as the ExternalSecret key"
  value       = module.rds.db_connection_secret_name
}

output "db_credentials_secret_name" {
  description = "Database username/password secret, used as database.credentialsSecret in the Helm values."
  value       = module.secrets_manager.db_credentials_secret_name
}
