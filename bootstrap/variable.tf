variable "state_bucket_name" {
  description = "Name of the S3 bucket for Terraform state"
  type        = string
  default     = "eks-v2-dev-tf-state"
}

variable "project_name" {
  description = "Project name, used to prefix resource names"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "github_org" {
  description = "GitHub organisation or username"
  type        = string
  default     = "EKS-V2-Alpha-Team"
}

variable "github_repo" {
  description = "GitHub repository name"
  type        = string
  default     = "Alpha-Pilot-4week"
}


variable "aws_region" {
  description = "AWS region to deploy resources"
  type        = string
  default     = "eu-west-2"
}

variable "github_org_id" {
  type    = string
  default = "331815569"
}

variable "github_repo_id" {
  type    = string
  default = "1373024865"
}
