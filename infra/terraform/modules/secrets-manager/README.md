# Secrets Manager Module

## Purpose

Creates AWS Secrets Manager resources for the EKS v2
application, encrypted using a customer-managed KMS key.

## Resources

- Customer-managed KMS key with automatic key rotation
- KMS alias
- Database credentials secret
- Grafana password secret
- Independently generated passwords

## Inputs

| Name | Description | Type |
|---|---|---|
| project_name | Project name used in resource naming | string |
| environment | Deployment environment | string |

## Outputs

| Name | Description |
|---|---|
| db_secret_arn | ARN of the database credentials secret |
| grafana_password_secret_arn | ARN of the Grafana password secret |

## Security

Generated passwords use ephemeral Terraform resources
and write-only Secrets Manager arguments.

KMS key rotation is enabled.

Automatic database and Grafana credential rotation
has not been implemented and requires a team decision.