variable "aws_region" {
  description = "AWS region to deploy resources"
  type        = string
  default     = "eu-west-2"
}

variable "project_name" {
  description = "This is the project name"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Deployment environment."
  type        = string
  default     = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of: dev, staging, prod."
  }
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.6.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "IDs of the public subnets"
  type        = list(string)
  default     = ["10.6.0.0/24", "10.6.1.0/24", "10.6.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "IDs of the private subnets"
  type        = list(string)
  default     = ["10.6.64.0/19", "10.6.96.0/19", "10.6.128.0/19"]
}

variable "db_subnet_cidrs" {
  description = "Name of the DB subnet group"
  type        = list(string)
  default     = ["10.6.32.0/24", "10.6.33.0/24", "10.6.34.0/24"]
}

variable "azs" {
  description = "Availability zones to create subnets in"
  type        = list(string)
  default     = ["eu-west-2a", "eu-west-2b", "eu-west-2c"]
}

variable "cluster_admin_principal_arns" {
  description = "IAM user/role ARNs to grant EKS cluster admin access via access entries"
  type        = list(string)
  default     = []
}

variable "db_name" {
  description = "Initial database RDS creates on launch"
  type        = string
  default     = "orders"
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
  default     = false
}

variable "maintenance_window" {
  description = "Weekly maintenance window (UTC)"
  type        = string
  default     = "sun:04:00-sun:05:00"
}

variable "instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.medium"
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
  description = "Admin user"
  type        = string
  default     = "admin"
}

variable "multi_az" {
  description = "Synchronous standby in a second AZ with automatic failover."
  type        = bool
  default     = false
}

variable "backup_retention_period" {
  description = "Days of automated backups / point-in-time recovery. 0 would disable backups entirely, so it is blocked."
  type        = number
  default     = 14
}

variable "backup_window" {
  description = "Daily backup window (UTC). Must not overlap maintenance_window."
  type        = string
  default     = "02:00-03:00"
}

variable "deletion_protection" {
  description = "Blocks deletion via API/console/Terraform."
  type        = bool
  default     = false
}

variable "delete_automated_backups" {
  description = "true = automated backups are deleted with the instance (dev). false = kept until retention expires (prod)."
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "a final snapshot is taken on deletion."
  type        = bool
  default     = true
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
