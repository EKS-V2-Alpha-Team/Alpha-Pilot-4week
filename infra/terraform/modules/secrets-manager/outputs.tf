output "grafana_password_secret_arn" {
  description = "ARN of the Grafana password secret"
  value       = aws_secretsmanager_secret.grafana_password_secret.arn
}

output "kms_key_arn" {
  description = "ARN of the KMS key used to encrypt secrets"
  value       = aws_kms_key.secret_encryption.arn
}
