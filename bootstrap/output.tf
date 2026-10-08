
output "github_actions_role_arn" {
  description = "ARN of the GitHub Actions IAM role for OIDC authentication"
  value       = aws_iam_role.github_actions.arn
}

output "state_bucket_name" {
  description = "S3 bucket name for Statefile"
  value       = aws_s3_bucket.terraform_state.id
}

output "ecr_repository_url" {
  description = "URL of ECR repository"
  value       = aws_ecr_repository.main.repository_url
}

output "ecr_repository_name" {
  description = "Name of the ECR repository"
  value       = aws_ecr_repository.main.name
}

output "route53_hosted_zone_name_servers" {
  description = "Name servers of the Route 53 hosted zone"
  value       = aws_route53_zone.main.name_servers
}

output "route53_hosted_zone_arn" {
  description = "ARN of the Route 53 hosted zone"
  value       = aws_route53_zone.main.arn
}

# new state

output "state_bucket_names" {
  description = "Terraform state bucket names, keyed by environment tier"
  value       = { for k, b in aws_s3_bucket.tf_state : k => b.bucket }
}
