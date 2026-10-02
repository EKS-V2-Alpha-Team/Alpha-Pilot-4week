# Platform Helm Configuration

This directory contains the Helmfile configuration for the Kubernetes platform components used by the EKS-V2 project.

## Components

- Traefik
- cert-manager / Let's Encrypt
- ExternalDNS
- ArgoCD
- Prometheus
- Grafana
- Karpenter

## Structure

- `helmfile.yaml` - Defines Helm repositories and releases.
- `helm-values/` - Component-specific Helm values.
- `cert-manager/` - Let's Encrypt ClusterIssuer configuration.
- `argocd/` - ArgoCD application configuration.

## Validation

Validate all configured releases:

```bash
helmfile lint