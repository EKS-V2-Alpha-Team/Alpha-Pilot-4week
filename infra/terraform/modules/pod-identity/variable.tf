variable "project_name" {
  description = "This is the project name"
  type        = string
  default     = "eks-v2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "db_credentials_secret_arn" {
  description = "ARN of the database credentials (username, password) secret"
  type        = string
}

variable "db_connection_secret_arn" {
  description = "ARN of the database connection (host, port) secret"
  type        = string
}

variable "grafana_secret_arn" {
  description = "ARN of the Grafana password secret"
  type        = string
}

variable "jwt_secret_arn" {
  description = "ARN of the api-gateway JWT signing secret"
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
  description = "Kubernetes namespace of External Secrets, which reads Secrets Manager"
  default     = "external-secrets"
}

variable "secrets_manager_service_account" {
  type        = string
  description = "Kubernetes service account of External Secrets"
  default     = "external-secrets"
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
  default     = "order-platform"
  description = "Namespace the app chart deploys order-service into (all 9 services share it)"
}

variable "order_service_service_account" {
  type        = string
  default     = "order-service"
  description = "order-service ServiceAccount; the app chart names it after the release"
}

variable "worker_namespace" {
  type        = string
  default     = "order-platform"
  description = "Namespace the app chart deploys worker into (all 9 services share it)"
}

variable "worker_service_account" {
  type        = string
  default     = "worker"
  description = "worker ServiceAccount; the app chart names it after the release"
}

variable "route53_hosted_zone_arn" {
  type        = string
  description = "ARN of the platform hosted zone; scopes the ExternalDNS and cert-manager record permissions"
}
