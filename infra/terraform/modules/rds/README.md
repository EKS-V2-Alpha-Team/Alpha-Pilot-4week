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

## Modules

No modules.

## Resources

| Name | Type |
| ---- | ---- |
| [aws_cloudwatch_log_group.postgres](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/cloudwatch_log_group) | resource |
| [aws_db_instance.this](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/db_instance) | resource |
| [aws_secretsmanager_secret.connection](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/secretsmanager_secret) | resource |
| [aws_secretsmanager_secret_version.connection](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/secretsmanager_secret_version) | resource |
| [aws_security_group.rds](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/security_group) | resource |
| [aws_vpc_security_group_ingress_rule.postgres](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/vpc_security_group_ingress_rule) | resource |

## Inputs

| Name | Description | Type | Default | Required |
| ---- | ----------- | ---- | ------- | :------: |
| <a name="input_allocated_storage"></a> [allocated\_storage](#input\_allocated\_storage) | Initial storage in GiB (gp3). | `number` | `50` | no |
| <a name="input_allow_major_version_upgrade"></a> [allow\_major\_version\_upgrade](#input\_allow\_major\_version\_upgrade) | Set true ONLY for the PR that performs a major upgrade, then set back to false. | `bool` | `false` | no |
| <a name="input_allowed_security_group_id"></a> [allowed\_security\_group\_id](#input\_allowed\_security\_group\_id) | Security group allowed to reach postgres | `string` | n/a | yes |
| <a name="input_apply_immediately"></a> [apply\_immediately](#input\_apply\_immediately) | false = changes wait for the maintenance window (prod). true = apply now (dev). | `bool` | n/a | yes |
| <a name="input_backup_retention_period"></a> [backup\_retention\_period](#input\_backup\_retention\_period) | Days of automated backups / point-in-time recovery. 0 would disable backups entirely, so it is blocked. | `number` | n/a | yes |
| <a name="input_backup_window"></a> [backup\_window](#input\_backup\_window) | Daily backup window (UTC). Must not overlap maintenance\_window. | `string` | `"02:00-03:00"` | no |
| <a name="input_db_name"></a> [db\_name](#input\_db\_name) | Initial database RDS creates on launch | `string` | `"orders"` | no |
| <a name="input_db_subnet_group_name"></a> [db\_subnet\_group\_name](#input\_db\_subnet\_group\_name) | Existing DB subnet group | `string` | n/a | yes |
| <a name="input_delete_automated_backups"></a> [delete\_automated\_backups](#input\_delete\_automated\_backups) | true = automated backups are deleted with the instance (dev). false = kept until retention expires (prod). | `bool` | n/a | yes |
| <a name="input_deletion_protection"></a> [deletion\_protection](#input\_deletion\_protection) | Blocks deletion via API/console/Terraform. | `bool` | n/a | yes |
| <a name="input_engine_version"></a> [engine\_version](#input\_engine\_version) | Postgres MAJOR version only | `string` | `"17"` | no |
| <a name="input_environment"></a> [environment](#input\_environment) | Deployment environment. | `string` | n/a | yes |
| <a name="input_instance_class"></a> [instance\_class](#input\_instance\_class) | RDS instance class. | `string` | n/a | yes |
| <a name="input_kms_key_arn"></a> [kms\_key\_arn](#input\_kms\_key\_arn) | KMS key for storage | `string` | n/a | yes |
| <a name="input_log_retention_days"></a> [log\_retention\_days](#input\_log\_retention\_days) | Retention for exported Postgres logs in CloudWatch. | `number` | `7` | no |
| <a name="input_maintenance_window"></a> [maintenance\_window](#input\_maintenance\_window) | Weekly maintenance window (UTC) | `string` | `"sun:04:00-sun:05:00"` | no |
| <a name="input_master_credentials_secret_arn"></a> [master\_credentials\_secret\_arn](#input\_master\_credentials\_secret\_arn) | Secrets Manager secret holding {"username", "password"} for the master user | `string` | n/a | yes |
| <a name="input_master_password_version"></a> [master\_password\_version](#input\_master\_password\_version) | Bump to push the current secret password to RDS (write-only) | `number` | `1` | no |
| <a name="input_master_username"></a> [master\_username](#input\_master\_username) | Admin user. Must match the username in master\_credentials\_secret\_arn. | `string` | n/a | yes |
| <a name="input_max_allocated_storage"></a> [max\_allocated\_storage](#input\_max\_allocated\_storage) | Storage autoscaling ceiling in GiB. Must be greater than allocated\_storage. | `number` | `200` | no |
| <a name="input_multi_az"></a> [multi\_az](#input\_multi\_az) | Synchronous standby in a second AZ with automatic failover. | `bool` | n/a | yes |
| <a name="input_performance_insights_enabled"></a> [performance\_insights\_enabled](#input\_performance\_insights\_enabled) | Whether to enable Performance Insights for the RDS instance. | `bool` | `true` | no |
| <a name="input_project_name"></a> [project\_name](#input\_project\_name) | Project name | `string` | n/a | yes |
| <a name="input_skip_final_snapshot"></a> [skip\_final\_snapshot](#input\_skip\_final\_snapshot) | true = no snapshot on deletion (staging). false = final snapshot taken (prod) | `bool` | n/a | yes |
| <a name="input_vpc_id"></a> [vpc\_id](#input\_vpc\_id) | VPC the database lives in. | `string` | n/a | yes |

## Outputs

| Name | Description |
| ---- | ----------- |
| <a name="output_db_address"></a> [db\_address](#output\_db\_address) | Hostname for application connections |
| <a name="output_db_connection_secret_arn"></a> [db\_connection\_secret\_arn](#output\_db\_connection\_secret\_arn) | ARN of the secret holding the RDS host and port |
| <a name="output_db_connection_secret_name"></a> [db\_connection\_secret\_name](#output\_db\_connection\_secret\_name) | Name of the secret holding the RDS host and port, used as the ExternalSecret key |
| <a name="output_db_port"></a> [db\_port](#output\_db\_port) | Postgres port |
| <a name="output_security_group_id"></a> [security\_group\_id](#output\_security\_group\_id) | Database security group ID. |
<!-- END_TF_DOCS -->
