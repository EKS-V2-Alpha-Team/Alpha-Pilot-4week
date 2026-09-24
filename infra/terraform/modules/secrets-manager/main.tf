resource "aws_kms_key" "secrets_manager" {
  description             = "KMS key for encrypting application secrets in AWS Secrets Manager"
  enable_key_rotation     = true
  deletion_window_in_days = 10
}

resource "aws_kms_alias" "eks_v2_secrets_manager" {
  name          = "alias/${var.project_name}-${var.environment}-secrets-manager"
  target_key_id = aws_kms_key.secrets_manager.key_id
}

resource "aws_secretsmanager_secret" "db_credentials_secret" {
  name       = "${var.project_name}-${var.environment}-db-credentials"
  kms_key_id = aws_kms_key.secrets_manager.arn
}

resource "aws_secretsmanager_secret" "grafana_password_secret" {
  name       = "${var.project_name}-${var.environment}-grafana-password"
  kms_key_id = aws_kms_key.secrets_manager.arn
}

ephemeral "aws_secretsmanager_random_password" "db_password" {
  password_length = 24
}

ephemeral "aws_secretsmanager_random_password" "grafana_password" {
  password_length = 24
}

resource "aws_secretsmanager_secret_version" "db_credentials" {
  secret_id = aws_secretsmanager_secret.db_credentials_secret.id

  secret_string_wo = jsonencode({
    username = "app"
    password = ephemeral.aws_secretsmanager_random_password.db_password.random_password
  })

  secret_string_wo_version = 1
}

resource "aws_secretsmanager_secret_version" "grafana_password" {
  secret_id = aws_secretsmanager_secret.grafana_password_secret.id

  secret_string_wo         = ephemeral.aws_secretsmanager_random_password.grafana_password.random_password
  secret_string_wo_version = 1
}
