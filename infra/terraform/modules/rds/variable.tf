variable "project_name" {
  description = "Project name"
  type        = string
}

variable "environment" {
  description = "Deployment environment."
  type        = string

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of: dev, staging, prod."
  }
}

variable "vpc_id" {
  description = "VPC the database lives in."
  type        = string
}

variable "db_name" {
  description = "Initial database RDS creates on launch"
  type        = string
  default     = "orders"
}

variable "db_subnet_group_name" {
  description = "Existing DB subnet group"
  type        = string
}

variable "allowed_security_group_id" {
  description = "Security group allowed to reach postgres"
  type        = string
}

variable "engine_version" {
  description = "Postgres MAJOR version only"
  type        = string
  default     = "17"
}

variable "allow_major_version_upgrade" {
  description = "Set true ONLY for the PR that performs a major upgrade, then set back to false."
  type        = bool
  default     = false
}

variable "apply_immediately" {
  description = "false = changes wait for the maintenance window (prod). true = apply now (dev)."
  type        = bool
}

variable "maintenance_window" {
  description = "Weekly maintenance window (UTC)"
  type        = string
  default     = "sun:04:00-sun:05:00"
}

variable "instance_class" {
  description = "RDS instance class."
  type        = string
}

variable "allocated_storage" {
  description = "Initial storage in GiB (gp3)."
  type        = number
  default     = 50
}

variable "max_allocated_storage" {
  description = "Storage autoscaling ceiling in GiB. Must be greater than allocated_storage."
  type        = number
  default     = 200
}

variable "master_username" {
  description = "Admin user. Must match the username in master_credentials_secret_arn."
  type        = string
}

variable "master_credentials_secret_arn" {
  description = "Secrets Manager secret holding {\"username\", \"password\"} for the master user"
  type        = string
}

variable "master_password_version" {
  description = "Bump to push the current secret password to RDS (write-only)"
  type        = number
  default     = 1
}

variable "multi_az" {
  description = "Synchronous standby in a second AZ with automatic failover."
  type        = bool
}

variable "backup_retention_period" {
  description = "Days of automated backups / point-in-time recovery. 0 would disable backups entirely, so it is blocked."
  type        = number
}

variable "backup_window" {
  description = "Daily backup window (UTC). Must not overlap maintenance_window."
  type        = string
  default     = "02:00-03:00"
}

variable "deletion_protection" {
  description = "Blocks deletion via API/console/Terraform."
  type        = bool
}

variable "delete_automated_backups" {
  description = "true = automated backups are deleted with the instance (dev). false = kept until retention expires (prod)."
  type        = bool
}

variable "skip_final_snapshot" {
  description = "true = no snapshot on deletion (staging). false = final snapshot taken (prod)"
  type        = bool
}

variable "performance_insights_enabled" {
  description = "Whether to enable Performance Insights for the RDS instance."
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "Retention for exported Postgres logs in CloudWatch."
  type        = number
  default     = 7
}

variable "kms_key_arn" {
  description = "KMS key for storage"
  type        = string
}
