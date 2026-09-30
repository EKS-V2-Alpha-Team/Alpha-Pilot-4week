output "db_address" {
  description = "Hostname for application connections"
  value       = aws_db_instance.this.address
}

output "db_port" {
  description = "Postgres port"
  value       = aws_db_instance.this.port
}

output "db_secret_arn" {
  description = "ARN of the RDS-managed master password secret in Secrets Manager"
  value       = aws_db_instance.this.master_user_secret[0].secret_arn
}

output "security_group_id" {
  description = "Database security group ID."
  value       = aws_security_group.rds.id
}
