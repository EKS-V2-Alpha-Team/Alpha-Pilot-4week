output "argocd_namespace" {
  description = "Namespace ArgoCD is installed in"
  value       = helm_release.argocd.namespace
}

output "argocd_chart_version" {
  description = "Installed argo-cd Helm chart version"
  value       = helm_release.argocd.version
}

output "repo_secret_name" {
  description = "Kubernetes Secret holding ArgoCD's credentials for the repo"
  value       = kubernetes_secret_v1.repo.metadata[0].name
}
