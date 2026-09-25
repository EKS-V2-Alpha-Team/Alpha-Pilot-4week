module "vpc" {
  source               = "./modules/vpc"
  project_name         = var.project_name
  aws_region           = var.aws_region
  vpc_cidr             = var.vpc_cidr
  azs                  = var.azs
  public_subnet_cidrs  = var.public_subnet_cidrs
  private_subnet_cidrs = var.private_subnet_cidrs
  db_subnet_cidrs      = var.db_subnet_cidrs
}

module "eks-cluster" {
  source = "./modules/eks-cluster"

}

module "karpenter" {
  source = "./modules/karpenter"

}

module "observability" {
  source = "./modules/observability"


}

module "pod-identity" {
  source = "./modules/pod-identity"

}

module "secrets_manager" {
  source = "./modules/secrets-manager"

}

