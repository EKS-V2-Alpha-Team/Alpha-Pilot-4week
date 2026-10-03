module "vpc" {
  source = "./modules/vpc"

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

  db_secret_arn        = module.rds.db_secret_arn
  grafana_secret_arn   = module.secrets_manager.grafana_password_secret_arn
  jwt_secret_arn       = module.secrets_manager.jwt_secret_arn
  project_name         = var.project_name
  cluster_name         = module.eks.cluster_name
  kms_key_arn          = module.secrets_manager.kms_key_arn
  environment          = var.environment
  aws_region           = var.aws_region
  worker_node_role_arn = module.eks.node_role_arn
  sqs_queue_arn        = module.sqs.queue_arn

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

module "rds" {
  source = "./modules/rds"

  project_name              = var.project_name
  environment               = var.environment
  vpc_id                    = module.vpc.vpc_id
  db_subnet_group_name      = module.vpc.db_subnet_group_name
  kms_key_arn               = module.secrets_manager.kms_key_arn
  allowed_security_group_id = module.eks.cluster_security_group_id
}
