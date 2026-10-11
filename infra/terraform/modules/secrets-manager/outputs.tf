output "grafana_password_secret_arn" {
  description = "ARN of the Grafana password secret"
  value       = aws_secretsmanager_secret.grafana_password_secret.arn
}

output "kms_key_arn" {
  description = "ARN of the KMS key used to encrypt secrets"
  value       = aws_kms_key.secret_encryption.arn
}

output "jwt_secret_arn" {
  description = "ARN of the api-gateway JWT signing secret"
  value       = aws_secretsmanager_secret.jwt_secret.arn
}

output "db_credentials_secret_arn" {
  description = "ARN of the database credentials secret"
  value       = aws_secretsmanager_secret_version.db_credentials.secret_arn
}

output "db_credentials_secret_name" {
  description = "Name of the database credentials secret, used as the ExternalSecret key"
  value       = aws_secretsmanager_secret.db_credentials.name
}

output "db_username" {
  description = "postgres master username stored in the database credentials secret"
  value       = var.db_username
}

output "db_connection_secret_name" {
  description = "Name of the db connection secret"
  value       = var.environment == "dev" ? aws_secretsmanager_secret.db_connection[0].name : null
}

output "db_connection_secret_arn" {
  description = "arn  of the db connection secret"
  value       = var.environment == "dev" ? aws_secretsmanager_secret.db_connection[0].arn : null
}

output "alertmanager_slack_secret_arn" {
  description = "ARN of the production alertmanager slack webhook secret"
  value       = var.environment == "prod" ? aws_secretsmanager_secret.alertmanager_slack_webhook[0].arn : null
}
