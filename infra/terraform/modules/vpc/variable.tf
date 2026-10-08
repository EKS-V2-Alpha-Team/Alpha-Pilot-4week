variable "project_name" {
  description = "Name of project"
  type        = string
  default     = "eks-v2"
}

variable "aws_region" {
  description = "Region VPC is in"
  type        = string
  default     = "eu-west-2"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
}

variable "public_subnet_cidrs" {
  description = "IDs of the public subnets"
  type        = list(string)

}

variable "private_subnet_cidrs" {
  description = "IDs of the private subnets"
  type        = list(string)
}

variable "db_subnet_cidrs" {
  description = "IDs of the db subnets"
  type        = list(string)
}

variable "azs" {
  description = "Availability zones to create subnets in"
  type        = list(string)
}
