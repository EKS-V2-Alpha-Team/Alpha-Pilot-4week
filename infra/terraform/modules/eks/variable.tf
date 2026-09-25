variable "project_name" {
  description = "Name of project"
  type        = string
  default     = "eks-v2"
}

variable "private_subnet_ids" {
  description = "Private subnet IDs"
  type        = list(string)
}