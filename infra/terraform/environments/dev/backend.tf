terraform {
  backend "s3" {
    bucket       = "eks-v2-nonprod-tfstate"
    key          = "dev/terraform.tfstate"
    region       = "eu-west-2"
    encrypt      = true
    use_lockfile = true
  }
}
