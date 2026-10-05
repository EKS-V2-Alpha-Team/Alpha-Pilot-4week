output "argocd_namespace" {
  description = "Namespace ArgoCD is installed in"
  value       = helm_release.argocd.namespace
}

output "argocd_chart_version" {
  description = "Installed argo-cd Helm chart version"
  value       = helm_release.argocd.version
}
