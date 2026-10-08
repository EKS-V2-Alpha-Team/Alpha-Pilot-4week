variable "project_name" {
  description = "Name of project"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "dlq_alarm_actions" {
  description = "Where the DLQ alarm notifies (for example an SNS topic ARN). Empty: the alarm only shows in CloudWatch"
  type        = list(string)
  default     = []
}
