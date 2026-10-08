variable "aws_region" {
  description = "AWS region to deploy resources"
  type        = string
  default     = "eu-west-2"
}

variable "project_name" {
  description = "Project name"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Deployment environment"
  type        = string
  default     = "prod"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be one of: dev, staging, prod."
  }
}

# Network
variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.8.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for the public subnets"
  type        = list(string)
  default     = ["10.8.0.0/24", "10.8.1.0/24", "10.8.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for the private subnets"
  type        = list(string)
  default     = ["10.8.64.0/19", "10.8.96.0/19", "10.8.128.0/19"]
}

variable "db_subnet_cidrs" {
  description = "CIDR blocks for the database subnets"
  type        = list(string)
  default     = ["10.8.32.0/24", "10.8.33.0/24", "10.8.34.0/24"]
}

variable "azs" {
  description = "Availability zones to create subnets in"
  type        = list(string)
  default     = ["eu-west-2a", "eu-west-2b", "eu-west-2c"]
}

# Access and DNS
variable "cluster_admin_principal_arns" {
  description = "Extra IAM user/role ARNs to grant EKS cluster admin access via access entries"
  type        = list(string)
  default     = []
}

variable "route53_hosted_zone_name" {
  description = "Route 53 hosted zone"
  type        = string
  default     = "eksv2.naimaosmann.com"
}

# EKS system node group (Karpenter manages workload nodes)
variable "node_instance_types" {
  description = "EC2 instance types for the EKS system node group"
  type        = list(string)
  default     = ["t3.medium"]
}

variable "node_desired_size" {
  description = "Desired number of nodes in the system node group"
  type        = number
  default     = 3
}

variable "node_min_size" {
  description = "Minimum number of nodes in the system node group"
  type        = number
  default     = 3
}

variable "node_max_size" {
  description = "Maximum number of nodes in the system node group"
  type        = number
  default     = 6
}

# RDS
variable "instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.m7g.large"
}

variable "multi_az" {
  description = "RDS: run a standby in a second AZ with automatic failover"
  type        = bool
  default     = true
}

variable "deletion_protection" {
  description = "RDS: block deletion of the instance"
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "RDS: skip the final snapshot when the instance is deleted"
  type        = bool
  default     = false
}

variable "delete_automated_backups" {
  description = "RDS: delete automated backups when the instance is deleted"
  type        = bool
  default     = false
}

variable "backup_retention_period" {
  description = "RDS: days of automated backups"
  type        = number
  default     = 14
}

variable "apply_immediately" {
  description = "RDS: apply changes now (true) or in the maintenance window (false)"
  type        = bool
  default     = false
}
