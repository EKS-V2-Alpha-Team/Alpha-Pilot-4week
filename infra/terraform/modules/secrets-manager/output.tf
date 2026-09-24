output "db_secret_arn" {
  description = "ARN of the database credentials secret"
  value       = aws_secretsmanager_secret.db_credentials_secret.arn
}

output "grafana_password_secret_arn" {
  description = "ARN of the Grafana password secret"
  value       = aws_secretsmanager_secret.grafana_password_secret.arn
}