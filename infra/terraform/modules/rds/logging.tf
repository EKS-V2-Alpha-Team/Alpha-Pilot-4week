resource "aws_cloudwatch_log_group" "postgres" {
  name              = "/aws/rds/instance/${var.project_name}-${var.environment}-postgres/postgresql"
  retention_in_days = var.log_retention_days

  tags = {
    Name = "${var.project_name}-postgres-logs"
  }
}
