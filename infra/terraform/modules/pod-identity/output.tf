output "secrets_manager_role_arn" {
  description = "ARN of the secrets manager pod identity role"
  value       = aws_iam_role.secrets_manager_pod_role.arn
}

output "sqs_role_arn" {
  description = "ARN of the sqs pod identity role"
  value       = aws_iam_role.sqs_pod_role.arn
}

output "external_dns_role_arn" {
  description = "ARN of the external dns pod identity role"
  value       = aws_iam_role.external_dns_pod_role.arn
}

output "cert_manager_role_arn" {
  description = "ARN of the cert manager pod identity role"
  value       = aws_iam_role.cert_manager_pod_role.arn
}