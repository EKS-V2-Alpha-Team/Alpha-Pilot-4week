resource "aws_security_group" "rds" {
  name        = "${var.project_name}-${var.environment}-postgres-sg"
  description = "Postgres access from allowed security groups only"
  vpc_id      = var.vpc_id

  tags = {
    Name = "${var.project_name}-postgres-sg"
  }
}

resource "aws_vpc_security_group_ingress_rule" "postgres" {
  description                  = "EKS cluster allowed access to PostgreSQL"
  security_group_id            = aws_security_group.rds.id
  referenced_security_group_id = var.allowed_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432

  tags = {
    Name = "${var.project_name}-postgres-from-eks"
  }
}
