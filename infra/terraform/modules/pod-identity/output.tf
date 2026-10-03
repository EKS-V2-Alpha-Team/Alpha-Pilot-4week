output "secrets_manager_role_arn" {
  description = "ARN of the secrets manager pod identity role"
  value       = aws_iam_role.secrets_manager_pod_role.arn
}

output "order_service_sqs_role_arn" {
  description = "ARN of the sqs order  identity role"
  value       = aws_iam_role.order_service_sqs_pod_role.arn
}

output "worker_sqs_role_arn" {
  description = "ARN of the sqs worker pod identity role"
  value       = aws_iam_role.worker_sqs_pod_role.arn
}

output "external_dns_role_arn" {
  description = "ARN of the external dns pod identity role"
  value       = aws_iam_role.external_dns_pod_role.arn
}

output "cert_manager_role_arn" {
  description = "ARN of the cert manager pod identity role"
  value       = aws_iam_role.cert_manager_pod_role.arn
}

output "aws_load_balancer_controller_role_arn" {
  description = "ARN of the aws load balancer controller pod identity role"
  value       = aws_iam_role.aws_load_balancer_controller_pod_role.arn
}
