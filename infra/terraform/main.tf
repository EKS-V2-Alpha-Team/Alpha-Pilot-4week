module "vpc" {
  source = "./modules/vpc"

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

