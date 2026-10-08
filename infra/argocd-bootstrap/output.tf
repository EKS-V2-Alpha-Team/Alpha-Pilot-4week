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

output "root_app_names" {
  description = "ArgoCD root Applications for dev, staging and prod"
  value       = [for env, app in local.root_apps : app.metadata.name]
}
