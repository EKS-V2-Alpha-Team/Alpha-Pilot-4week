resource "aws_kms_key" "secret_encryption" {
  description             = "KMS key for encrypting application secrets in AWS Secrets Manager"
  enable_key_rotation     = true
  deletion_window_in_days = 10
}

resource "aws_kms_alias" "eks_v2_secrets_manager" {
  name          = "alias/${var.project_name}-${var.environment}-secrets-manager"
  target_key_id = aws_kms_key.secret_encryption.key_id
}

resource "aws_secretsmanager_secret" "grafana_password_secret" {
  name       = "${var.project_name}-${var.environment}-grafana-password"
  kms_key_id = aws_kms_key.secret_encryption.arn

  # dev only: delete immediately so destroy/apply cycles can reuse the name; prod keeps the default 30-day recovery window
  recovery_window_in_days = 0
}

ephemeral "aws_secretsmanager_random_password" "grafana_password" {
  password_length = 24
}

resource "aws_secretsmanager_secret_version" "grafana_password" {
  secret_id = aws_secretsmanager_secret.grafana_password_secret.id

  secret_string_wo         = ephemeral.aws_secretsmanager_random_password.grafana_password.random_password
  secret_string_wo_version = 1
}

resource "aws_secretsmanager_secret" "jwt_secret" {
  name       = "${var.project_name}-${var.environment}-jwt-secret"
  kms_key_id = aws_kms_key.secret_encryption.arn

  # dev only: delete immediately so destroy/apply cycles can reuse the name; prod keeps the default 30-day recovery window
  recovery_window_in_days = 0
}

ephemeral "aws_secretsmanager_random_password" "jwt_secret" {
  password_length = 64
}

resource "aws_secretsmanager_secret_version" "jwt_secret" {
  secret_id = aws_secretsmanager_secret.jwt_secret.id

  secret_string_wo         = ephemeral.aws_secretsmanager_random_password.jwt_secret.random_password
  secret_string_wo_version = 1
}

resource "aws_secretsmanager_secret" "db_credentials" {
  name       = "${var.project_name}-${var.environment}-db-credentials"
  kms_key_id = aws_kms_key.secret_encryption.arn

  # dev only: delete immediately so destroy/apply cycles can reuse the name; prod keeps the default 30-day recovery window
  recovery_window_in_days = var.environment == "dev" ? 0 : 30
}

ephemeral "aws_secretsmanager_random_password" "db_password" {
  password_length = 32
  # RDS rejects '/', '@', '"' and spaces in the master password.
  exclude_characters = "/@\" "
}

resource "aws_secretsmanager_secret_version" "db_credentials" {
  secret_id = aws_secretsmanager_secret.db_credentials.id

  secret_string_wo = jsonencode({
    username = var.db_username
    password = ephemeral.aws_secretsmanager_random_password.db_password.random_password
  })
  secret_string_wo_version = 1
}

# creating db host and port connection
resource "aws_secretsmanager_secret" "db_connection" {
  count = var.environment == "dev" ? 1 : 0 # will create secret only if using dev envvironment

  name       = "${var.project_name}-${var.environment}-db-connection"
  kms_key_id = aws_kms_key.secret_encryption.arn

  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "db_connection" {
  count = var.environment == "dev" ? 1 : 0

  secret_id = aws_secretsmanager_secret.db_connection[0].id

  secret_string = jsonencode({
    host = "postgres.order-platform.svc.cluster.local"
    port = 5432
  })
}

resource "aws_secretsmanager_secret" "alertmanager_slack_webhook" {
  count = var.environment == "prod" ? 1 : 0

  name       = "${var.project_name}-${var.environment}-alertmanager-slack-webhook"
  kms_key_id = aws_kms_key.secret_encryption.arn

  recovery_window_in_days = 30
}
