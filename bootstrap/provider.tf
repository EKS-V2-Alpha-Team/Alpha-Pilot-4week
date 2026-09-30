terraform {
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.65"
    }
  }

  #   backend "s3" {
  #     bucket       = "eks-v2-dev-tf-state"
  #     key          = "bootstrap/terraform.tfstate"
  #     region       = "eu-west-2"
  #     encrypt      = true
  #     use_lockfile = true
  #   }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

