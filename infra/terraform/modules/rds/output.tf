output "db_address" {
  description = "Hostname for application connections"
  value       = aws_db_instance.this.address
}

output "db_port" {
  description = "Postgres port"
  value       = aws_db_instance.this.port
}

output "db_connection_secret_arn" {
  description = "ARN of the secret holding the RDS host and port"
  value       = aws_secretsmanager_secret_version.connection.secret_arn
}

output "db_connection_secret_name" {
  description = "Name of the secret holding the RDS host and port, used as the ExternalSecret key"
  value       = aws_secretsmanager_secret.connection.name
}

output "security_group_id" {
  description = "Database security group ID."
  value       = aws_security_group.rds.id
}
