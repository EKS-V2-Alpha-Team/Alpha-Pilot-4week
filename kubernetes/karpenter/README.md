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

The EC2NodeClass currently uses:
- EKS cluster discovery name `eks-v2-dev-cluster`.
- EC2 node IAM role `eks-v2-dev-node-role`.
- Amazon Linux 2023 AMI release `al2023@v20260930`.
- Project tag `eks-v2`.

## Outstanding Dependencies

The following still require confirmation or implementation:

- Matching subnet and security group discovery tags.
- Deployment order and ArgoCD integration.

The Karpenter controller is installed separately
through ALPHA-62.

Karpenter controller permissions are configured
through EKS Pod Identity.

## Validation

Both manifests have passed local YAML syntax
validation.

Kubernetes schema validation and deployment
testing are still outstanding.

## Implementation Notes

Karpenter's EC2NodeClass and NodePool are managed through Kubernetes manifests rather than the previous Terraform Karpenter module.

The existing Terraform Karpenter module files have therefore been removed as part of this ticket.

The remaining infrastructure dependency is ensuring
the appropriate subnet and security group resources
have matching `karpenter.sh/discovery` tags for
`eks-v2-dev-cluster`.

Deployment order and ArgoCD integration also need
to be confirmed before deployment testing can be
completed.