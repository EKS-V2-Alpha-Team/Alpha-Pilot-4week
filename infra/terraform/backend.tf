terraform {
  backend "s3" {
    bucket       = "eks-v2-dev-tf-state"
    key          = "terraform/terraform.tfstate"
    region       = "eu-west-2"
    encrypt      = true
    use_lockfile = true
  }
}