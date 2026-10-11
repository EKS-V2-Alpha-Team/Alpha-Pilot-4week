# vpc_cni uses the node role's AmazonEKS_CNI_Policy without it nodes never become Ready.
# once Pod Identity role is created it will be replaced with that role.
resource "aws_eks_addon" "vpc_cni" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "vpc-cni"
}

resource "aws_eks_addon" "kube_proxy" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "kube-proxy"
}

resource "aws_eks_addon" "coredns" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "coredns"

  depends_on = [aws_eks_node_group.this]
}

resource "aws_eks_addon" "pod_identity_agent" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "eks-pod-identity-agent"
}

resource "aws_eks_addon" "ebs_csi" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "aws-ebs-csi-driver"

  pod_identity_association {
    role_arn        = aws_iam_role.ebs_csi.arn
    service_account = "ebs-csi-controller-sa"
  }

  depends_on = [
    aws_eks_node_group.this,
    aws_eks_addon.pod_identity_agent,
    aws_iam_role_policy_attachment.ebs_csi_AmazonEBSCSIDriverPolicy,
  ]
}

# Installs the VolumeSnapshot CRDs and the controller that turns a VolumeSnapshot into an
# EBS snapshot (through the EBS CSI driver above). Needed before any VolumeSnapshotClass syncs.
resource "aws_eks_addon" "snapshot_controller" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "snapshot-controller"

  depends_on = [aws_eks_node_group.this]
}
