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

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.6.0.0/16"

}

variable "public_subnet_cidrs" {
  description = "IDs of the public subnets"
  type        = list(string)
  default     = ["10.6.0.0/24", "10.6.1.0/24", "10.6.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "IDs of the private subnets"
  type        = list(string)
  default     = ["10.6.64.0/19", "10.6.96.0/19", "10.6.128.0/19"]
}

variable "db_subnet_cidrs" {
  description = "IDs of the db subnets"
  type        = list(string)
  default     = ["10.6.32.0/24", "10.6.33.0/24", "10.6.34.0/24"]
}

variable "azs" {
  description = "Availability zones to create subnets in"
  type    = list(string)
  default = ["eu-west-2a", "eu-west-2b", "eu-west-2c"]
}
