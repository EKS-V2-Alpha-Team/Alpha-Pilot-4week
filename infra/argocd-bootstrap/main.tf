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
