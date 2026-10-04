# Secrets Manager Module

## Purpose

Creates AWS Secrets Manager resources for the EKS v2
application, encrypted using a customer-managed KMS key.

## Resources

- Customer-managed KMS key with automatic key rotation
- KMS alias
- Grafana password secret
- api-gateway JWT signing secret (`<project>-<environment>-jwt-secret`)
- Database credentials secret (`<project>-<environment>-db-credentials`), JSON `{"username", "password"}`
- Independently generated values for each secret

The rds module reads the database password from this secret, so RDS and the apps use the same value. The fixed name means the Helm values reference it once per environment instead of a new ARN after every rebuild.

## Inputs

| Name | Description | Type |
|---|---|---|
| project_name | Project name used in resource naming | string |
| environment | Deployment environment | string |
| db_username | RDS master username stored in the database credentials secret (default `admin`) | string |

## Outputs

| Name | Description |
|---|---|
| db_credentials_secret_arn | ARN of the database credentials secret |
| db_credentials_secret_name | Name of the database credentials secret, used as the ExternalSecret key |
| db_username | RDS master username stored in the secret |
| grafana_password_secret_arn | ARN of the Grafana password secret |
| jwt_secret_arn | ARN of the api-gateway JWT signing secret |
| kms_key_arn | ARN of the KMS key used to encrypt secrets |

## Local Testing

The module was tested using the team's local Floci
development environment.

### Commands

Run from the repository root:

```bash
./scripts/floci-local-test.sh -target=module.secrets_manager
```

```bash
terraform -chdir=infra/terraform/local apply \
  -target=module.secrets_manager
```

### Results

- Standalone Terraform validation passed.
- Local testing performed using Floci.
- AWS deployment has not been tested.

Note: Successful local execution does not independently
verify that Floci supports every Secrets Manager and KMS
operation used by this module.

## Security

Generated passwords use ephemeral Terraform resources
and write-only Secrets Manager arguments.

KMS key rotation is enabled.

All secrets set `recovery_window_in_days = 0` so a destroy deletes them immediately and the next apply can reuse the names. That suits dev, where we deploy and destroy repeatedly. In prod, keep the default 30-day recovery window so a deleted secret can be restored.

Automatic database and Grafana credential rotation
has not been implemented and requires a team decision.