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

variable "argocd_chart_version" {
  description = "argo-cd Helm chart version"
  type        = string
  default     = "10.9.6"
}
