resource "aws_eks_cluster" "this" {
  name = "${var.project_name}-${var.environment}-cluster"

  access_config {
    authentication_mode = "API"
  }

  role_arn = aws_iam_role.cluster.arn
  version  = "1.35"

  bootstrap_self_managed_addons = false

  vpc_config {
    subnet_ids = var.private_subnet_ids
  }

  encryption_config {
    provider {
      key_arn = aws_kms_key.cluster-kms.arn
    }
    resources = ["secrets"]
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  # Ensure that IAM Role permissions and the log group are created before and
  # deleted after EKS Cluster handling. Otherwise, EKS will not be able to
  # properly delete EKS managed EC2 infrastructure such as Security Groups,
  # and control plane logs would land in an auto-created, unmanaged log group.
  depends_on = [
    aws_iam_role_policy_attachment.cluster_AmazonEKSClusterPolicy,
    aws_cloudwatch_log_group.logs,
  ]

  tags = {
    Name = "${var.project_name}-${var.environment}-cluster"
  }
}
