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

  data = {
    type = "git"
    url  = var.repo_url
  }

  data_wo = {
    sshPrivateKey = ephemeral.aws_secretsmanager_secret_version.deploy_key.secret_string
  }
  data_wo_revision = var.deploy_key_version
}
