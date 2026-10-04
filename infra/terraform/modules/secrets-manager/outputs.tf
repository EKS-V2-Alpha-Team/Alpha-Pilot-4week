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
  # References the version so readers wait until the value has been written.
  value = aws_secretsmanager_secret_version.db_credentials.secret_arn
}

output "db_credentials_secret_name" {
  description = "Name of the database credentials secret, used as the ExternalSecret key"
  value       = aws_secretsmanager_secret.db_credentials.name
}

output "db_username" {
  description = "RDS master username stored in the database credentials secret"
  value       = var.db_username
}
