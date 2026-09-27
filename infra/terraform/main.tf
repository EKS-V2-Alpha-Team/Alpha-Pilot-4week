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

module "eks" {
  source = "./modules/eks"

  project_name                 = var.project_name
  environment                  = var.environment
  private_subnet_ids           = module.vpc.private_subnet_ids
  cluster_admin_principal_arns = var.cluster_admin_principal_arns
}

module "pod-identity" {
  source = "./modules/pod-identity"

  db_secret_arn = module.secrets_manager.db_secret_arn
  cluster_name = module.eks.cluster_name
  kms_key_arn = module.secrets_manager.kms_key_arn
  #sqs_queue_arn = = this needs output from sqs arn

}

module "secrets_manager" {
  source = "./modules/secrets-manager"

  project_name = var.project_name
  environment  = var.environment
}

module "sqs" {
  source = "./modules/sqs"

  project_name = var.project_name
  environment  = var.environment
}
