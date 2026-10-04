variable "project_name" {
  description = "Name of the project"
  type        = string
}

variable "environment" {
  description = "Deployment environment"
  type        = string
}

variable "db_username" {
  description = "RDS master username stored in the database credentials secret"
  type        = string
  default     = "admin"
}
