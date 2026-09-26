terraform {
  required_version = ">= 1.14.0"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.65" }
  }
  backend "s3" {
    bucket                      = "alpha-local-tfstate"
    key                         = "local/terraform.tfstate"
    region                      = "us-east-1"
    use_path_style              = true
    skip_credentials_validation = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    use_lockfile                = false
    access_key                  = "test"
    secret_key                  = "test"
    endpoints = {
      s3 = "http://localhost:4566"
    }
  }
}

provider "aws" {
  region     = "us-east-1"
  access_key = "test"
  secret_key = "test"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    ec2            = "http://localhost:4566"
    sts            = "http://localhost:4566"
    sqs            = "http://localhost:4566"
    secretsmanager = "http://localhost:4566"
    kms            = "http://localhost:4566"
  }
}

# same modules, same sources as infra/terraform/main.tf — mirrors the real composition
module "vpc" {
  source = "../modules/vpc"
}

module "eks-cluster" {
  source = "../modules/eks-cluster"
}

module "pod-identity" {
  source = "../modules/pod-identity"
}

module "secrets_manager" {
  source       = "../modules/secrets-manager"
  project_name = "eks-v2"
  environment  = "dev"
}