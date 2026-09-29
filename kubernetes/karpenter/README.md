# Karpenter Configuration

This directory contains the Kubernetes manifests for
Karpenter node provisioning on Amazon EKS.

## Resources

- ec2nodeclass.yaml: Configures AWS infrastructure
  used to launch EC2 instances.
- nodepool.yaml: Defines instance selection,
  provisioning limits and disruption policies.

## Configuration

The NodePool currently uses:
- Linux AMD64 instances.
- On-Demand capacity.
- CPU-only instance categories.
- A maximum of 16 vCPUs and 64Gi memory.
- Automatic consolidation.

No taints are configured for this general-purpose
NodePool. Workload isolation requirements are
subject to team confirmation.

## Outstanding Dependencies

The following require confirmation:

- EKS cluster name.
- EC2 node IAM role.
- Compatible Amazon Linux 2023 AMI version.
- Subnet and security group discovery tags.
- Karpenter controller IAM permissions.
- Deployment order and ArgoCD integration.

The Karpenter controller is installed separately
through ALPHA-62.

## Validation

Both manifests have passed local YAML syntax
validation.

Kubernetes schema validation and deployment
testing are still outstanding.

## Implementation Notes

Karpenter's EC2NodeClass and NodePool are managed through Kubernetes manifests rather than the previous Terraform Karpenter module.

The existing Terraform Karpenter module files have therefore been removed as part of this ticket.

The following configuration remains dependent on other infrastructure components:

- Confirmation of the EC2 node IAM role.
- Matching subnet and security group discovery tags.
- Karpenter controller installation and IAM permissions.
- Confirmation of deployment order and ArgoCD integration.

These dependencies must be resolved before deployment testing can be completed.