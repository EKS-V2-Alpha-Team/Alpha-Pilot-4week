variable "project_name" {
  description = "This is the project name"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "db_secret_arn" {
  description = "Arn of the database secret"
  type        = string
}


variable "kms_key_arn" {
  description = "ARN of the KMS key used to encrypt secrets"
  type        = string
}

variable "cluster_name" {
  description = "This is the eks clusters name"
  type        = string
}

variable "secrets_manager_namespace" {
  type        = string
  description = "Kubernetes namespace for workload that needs secrets manager"
  default     = "secrets_manager" # we will need to specify the namespace  once we have figured out what we will name them - this is temporary
}

variable "secrets_manager_service_account" {
  type        = string
  description = "Kubernetes svc account for workload that needs secrets manager"
  default     = "secrets_manager" # we will need to specify the svc account once we have figured out what we will name them - this is temporary
}

variable "aws_region" {
  description = "AWS region to deploy resources"
  type        = string
}

variable "worker_node_role_arn" {
  description = "this is the iam role of the worker nodes"
  type        = string
}

variable "sqs_queue_arn" {
  type        = string
  description = "this is the arn of the sqs queue"
}

variable "order_service_namespace" {
  type        = string
  default     = "order-service" #placeholder
  description = "this is the order service app namespace"
}

variable "order_service_service_account" {
  type        = string
  default     = "order-service" #placeholder
  description = "this is the order service app svc account"
}

variable "worker_namespace" {
  type        = string
  default     = "worker" #placeholder
  description = "this is the worker app namespace"
}

variable "worker_service_account" {
  type        = string
  default     = "worker" #placeholder
  description = "this is the worker app svc account"
}

variable "route53_hosted_zone_arn" {
  type        = string
  description = "ARN of the specific hosted zone externaldns will update/delete etc"
  default = "n/a" #placeholder
}
