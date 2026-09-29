variable "project_name" {
  description = "Name of project"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}
