terraform {
  backend "s3" {
    bucket       = "eks-v2-prod-tfstate"
    key          = "prod/terraform.tfstate"
    region       = "eu-west-2"
    encrypt      = true
    use_lockfile = true
  }
}
