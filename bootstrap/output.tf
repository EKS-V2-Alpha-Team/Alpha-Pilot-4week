
output "github_actions_role_arns" {
  description = "GitHub Actions role per environment. Set each as AWS_ROLE_ARN in the matching GitHub environment."
  value       = { for env, role in aws_iam_role.github_actions : env => role.arn }
}

output "github_actions_plan_role_arn" {
  description = "Read-only role for Terraform Plan. Set as the AWS_PLAN_ROLE_ARN repository variable."
  value       = aws_iam_role.github_actions_plan.arn
}

output "github_actions_ecr_role_arn" {
  description = "Image build and push role for CI. Set as the AWS_ECR_ROLE_ARN repository variable."
  value       = aws_iam_role.github_actions_ecr.arn
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
