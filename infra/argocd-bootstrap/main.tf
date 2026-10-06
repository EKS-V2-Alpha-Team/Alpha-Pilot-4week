locals {
  root_app = yamldecode(file("${path.module}/../../kubernetes/platform/root-app.yaml"))
}

# Same name the eks module gives the cluster in infra/terraform.
data "aws_eks_cluster" "this" {
  name = "${var.project_name}-${var.environment}-cluster"
}

resource "helm_release" "argocd" {
  name             = "argocd"
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = var.argocd_chart_version
  namespace        = "argocd"
  create_namespace = true
  wait             = true
  timeout          = 600

  values = [file("${path.module}/../../kubernetes/helm-values/argocd-values.yaml")]
}

# Created once by hand (outside Terraform, so it survives destroy). Ephemeral, so the
# private key never lands in state.
ephemeral "aws_secretsmanager_secret_version" "deploy_key" {
  secret_id = "${var.project_name}-${var.environment}-argocd-deploy-key"
}

# Lets ArgoCD clone this private repo. The label is how ArgoCD finds repo credentials.
resource "kubernetes_secret_v1" "repo" {
  metadata {
    name      = "alpha-repo"
    namespace = helm_release.argocd.namespace
    labels = {
      "argocd.argoproj.io/secret-type" = "repository"
    }
  }

  # bump deploy_key_version after changing any of the values in data_wo
  data_wo = {
    type          = "git"
    url           = var.repo_url
    sshPrivateKey = ephemeral.aws_secretsmanager_secret_version.deploy_key.secret_string
  }
  data_wo_revision = var.deploy_key_version
}

# Root app-of-apps, read from kubernetes/platform/root-app.yaml
resource "helm_release" "root_app" {
  name       = "argocd-root-app"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argocd-apps"
  version    = var.argocd_apps_chart_version
  namespace  = helm_release.argocd.namespace

  values = [yamlencode({
    applications = {
      (local.root_app.metadata.name) = merge(
        { namespace = local.root_app.metadata.namespace },
        local.root_app.spec,
      )
    }
  })]

  # ArgoCD can't sync the root app until it can read the repo.
  depends_on = [kubernetes_secret_v1.repo]
}
