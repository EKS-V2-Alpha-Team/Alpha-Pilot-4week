# Connection details for the apps, written once the instance exists. External Secrets
# reads this by its fixed name, so the Helm values never need the RDS address.
# Separate from the credentials secret: that one must exist before RDS (it holds the
# master password), so it cannot also hold the address RDS gets after creation.
resource "aws_secretsmanager_secret" "connection" {
  name       = "${var.project_name}-${var.environment}-db-connection"
  kms_key_id = var.kms_key_arn

  # dev only: delete immediately so destroy/apply cycles can reuse the name; prod keeps the default 30-day recovery window
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "connection" {
  secret_id = aws_secretsmanager_secret.connection.id

  # Not sensitive, so a normal (stateful) value: Terraform rewrites it if the address changes.
  secret_string = jsonencode({
    host = aws_db_instance.this.address
    port = aws_db_instance.this.port
  })
}
