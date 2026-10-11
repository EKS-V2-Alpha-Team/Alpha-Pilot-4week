<!-- BEGIN_TF_DOCS -->
## Requirements

| Name | Version |
| ---- | ------- |
| <a name="requirement_terraform"></a> [terraform](#requirement\_terraform) | >= 1.16.0 |
| <a name="requirement_aws"></a> [aws](#requirement\_aws) | >= 6.0 |

## Providers

| Name | Version |
| ---- | ------- |
| <a name="provider_aws"></a> [aws](#provider\_aws) | 6.68.0 |


## Resources

| Name | Type |
| ---- | ---- |
| [aws_eks_pod_identity_association.aws_load_balancer_controller](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_eks_pod_identity_association.cert_manager](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_eks_pod_identity_association.external_dns](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_eks_pod_identity_association.karpenter](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_eks_pod_identity_association.order_service_sqs](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_eks_pod_identity_association.secrets_manager](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_eks_pod_identity_association.worker_sqs](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eks_pod_identity_association) | resource |
| [aws_iam_policy.aws_load_balancer_controller](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_policy.cert_manager](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_policy.external_dns](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_policy.karpenter_policy](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_policy.order_service_sqs](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_policy.secrets_manager_read](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_policy.worker_sqs](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_policy) | resource |
| [aws_iam_role.aws_load_balancer_controller_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.cert_manager_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.external_dns_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.karpenter_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.order_service_sqs_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.secrets_manager_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role.worker_sqs_pod_role](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role) | resource |
| [aws_iam_role_policy_attachment.aws_load_balancer_controller_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_role_policy_attachment.cert_manager_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_role_policy_attachment.external_dns_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_role_policy_attachment.karpenter_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_role_policy_attachment.order_service_sqs_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_role_policy_attachment.secrets_manager_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_iam_role_policy_attachment.worker_sqs_attachment](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_role_policy_attachment) | resource |
| [aws_caller_identity.current](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/caller_identity) | data source |

## Inputs

| Name | Description | Type | Default | Required |
| ---- | ----------- | ---- | ------- | :------: |
| <a name="input_aws_region"></a> [aws\_region](#input\_aws\_region) | AWS region to deploy resources | `string` | n/a | yes |
| <a name="input_cluster_name"></a> [cluster\_name](#input\_cluster\_name) | This is the eks clusters name | `string` | n/a | yes |
| <a name="input_db_connection_secret_arn"></a> [db\_connection\_secret\_arn](#input\_db\_connection\_secret\_arn) | ARN of the database connection (host, port) secret | `string` | `null` | no |
| <a name="input_db_credentials_secret_arn"></a> [db\_credentials\_secret\_arn](#input\_db\_credentials\_secret\_arn) | ARN of the database credentials (username, password) secret | `string` | n/a | yes |
| <a name="input_environment"></a> [environment](#input\_environment) | Environment name (dev, staging, prod) | `string` | n/a | yes |
| <a name="input_grafana_secret_arn"></a> [grafana\_secret\_arn](#input\_grafana\_secret\_arn) | ARN of the Grafana password secret | `string` | n/a | yes |
| <a name="input_jwt_secret_arn"></a> [jwt\_secret\_arn](#input\_jwt\_secret\_arn) | ARN of the api-gateway JWT signing secret | `string` | n/a | yes |
| <a name="input_kms_key_arn"></a> [kms\_key\_arn](#input\_kms\_key\_arn) | ARN of the KMS key used to encrypt secrets | `string` | n/a | yes |
| <a name="input_order_service_namespace"></a> [order\_service\_namespace](#input\_order\_service\_namespace) | Namespace the app chart deploys order-service into (all 9 services share it) | `string` | `"order-platform"` | no |
| <a name="input_order_service_service_account"></a> [order\_service\_service\_account](#input\_order\_service\_service\_account) | order-service ServiceAccount; the app chart names it after the release | `string` | `"order-service"` | no |
| <a name="input_project_name"></a> [project\_name](#input\_project\_name) | This is the project name | `string` | `"eks-v2"` | no |
| <a name="input_route53_hosted_zone_arn"></a> [route53\_hosted\_zone\_arn](#input\_route53\_hosted\_zone\_arn) | ARN of the platform hosted zone; scopes the ExternalDNS and cert-manager record permissions | `string` | n/a | yes |
| <a name="input_secrets_manager_namespace"></a> [secrets\_manager\_namespace](#input\_secrets\_manager\_namespace) | Kubernetes namespace of External Secrets, which reads Secrets Manager | `string` | `"external-secrets"` | no |
| <a name="input_secrets_manager_service_account"></a> [secrets\_manager\_service\_account](#input\_secrets\_manager\_service\_account) | Kubernetes service account of External Secrets | `string` | `"external-secrets"` | no |
| <a name="input_sqs_queue_arn"></a> [sqs\_queue\_arn](#input\_sqs\_queue\_arn) | this is the arn of the sqs queue | `string` | n/a | yes |
| <a name="input_worker_namespace"></a> [worker\_namespace](#input\_worker\_namespace) | Namespace the app chart deploys worker into (all 9 services share it) | `string` | `"order-platform"` | no |
| <a name="input_worker_node_role_arn"></a> [worker\_node\_role\_arn](#input\_worker\_node\_role\_arn) | this is the iam role of the worker nodes | `string` | n/a | yes |
| <a name="input_worker_service_account"></a> [worker\_service\_account](#input\_worker\_service\_account) | worker ServiceAccount; the app chart names it after the release | `string` | `"worker"` | no |

## Outputs

| Name | Description |
| ---- | ----------- |
| <a name="output_aws_load_balancer_controller_role_arn"></a> [aws\_load\_balancer\_controller\_role\_arn](#output\_aws\_load\_balancer\_controller\_role\_arn) | ARN of the aws load balancer controller pod identity role |
| <a name="output_cert_manager_role_arn"></a> [cert\_manager\_role\_arn](#output\_cert\_manager\_role\_arn) | ARN of the cert manager pod identity role |
| <a name="output_external_dns_role_arn"></a> [external\_dns\_role\_arn](#output\_external\_dns\_role\_arn) | ARN of the external dns pod identity role |
| <a name="output_order_service_sqs_role_arn"></a> [order\_service\_sqs\_role\_arn](#output\_order\_service\_sqs\_role\_arn) | ARN of the sqs order  identity role |
| <a name="output_secrets_manager_role_arn"></a> [secrets\_manager\_role\_arn](#output\_secrets\_manager\_role\_arn) | ARN of the secrets manager pod identity role |
| <a name="output_worker_sqs_role_arn"></a> [worker\_sqs\_role\_arn](#output\_worker\_sqs\_role\_arn) | ARN of the sqs worker pod identity role |
<!-- END_TF_DOCS -->
