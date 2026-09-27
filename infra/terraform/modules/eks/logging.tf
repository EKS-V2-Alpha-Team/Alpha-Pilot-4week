resource "aws_cloudwatch_log_group" "logs" {
  name              = "/aws/eks/${var.project_name}-cluster/cluster"
  retention_in_days = 30
}
