variable "project_name" {
  description = "This is the project name"
  type        = string
  default     = "eks-v2"
}

variable "db_secret_arn" {
  description = "Arn of the database secret"  
  type        = string  
}


variable "kms_key_arn" {
 description = "ARN of the KMS key used to encrypt secrets"
 type = string
}

variable "cluster_name" {
  description = "This is the eks clusters name"  
  type        = string
}

variable "secrets_manager_namespace" {
  type        = string
  description = "Kubernetes namespace for workload that needs secrets manager"
  # we will need to specify the namespace  once we have figured out what we will name them 
}

variable "secrets_manager_service_account" {
  type        = string
  description = "Kubernetes svc account for workload that needs secrets manager"
  # we will need to specify the svc account once we have figured out what we will name them 
}

variable "sqs_queue_arn" {
  type        = string
  description = "this is the arn of the sqs queue"
}

variable "sqs_namespace" {
  type        = string
  description = "Kubernetes namespace for workload that needs sqs"
   # we will need to specify the namespace  once we have figured out what we will name them 
}

variable "sqs_service_account" {
  type        = string
  description = "Kubernetes svc account for workload that needs sqs"
    # we will need to specify the svc account once we have figured out what we will name them 
}

variable "route53_hosted_zone_arn" {
  type = string
  description = "ARN of the specific hosted zone externaldns will update/delete etc"
}