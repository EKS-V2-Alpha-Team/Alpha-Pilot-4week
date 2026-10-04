output "db_address" {
  description = "Hostname for application connections"
  value       = aws_db_instance.this.address
}

output "db_port" {
  description = "Postgres port"
  value       = aws_db_instance.this.port
}

output "security_group_id" {
  description = "Database security group ID."
  value       = aws_security_group.rds.id
}
