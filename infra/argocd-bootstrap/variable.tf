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

variable "argocd_apps_chart_version" {
  description = "argocd-apps Helm chart version (installs kubernetes/platform/root app)"
  type        = string
  default     = "2.0.6"
}

variable "repo_url" {
  description = "Git repo ArgoCD syncs from."
  type        = string
  default     = "git@github.com:EKS-V2-Alpha-Team/Alpha-Pilot-4week.git"
}

variable "deploy_key_version" {
  description = "Bump to re-send the repo secret (deploy key, repo_url) to the cluster"
  type        = number
  default     = 1
}
