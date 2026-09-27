resource "aws_kms_key" "cluster-kms" {
  description         = "Envelope encryption key for ${var.project_name} EKS secrets"
  enable_key_rotation = true
}

resource "aws_kms_alias" "cluster-kms" {
  name          = "alias/${var.project_name}-eks-kms"
  target_key_id = aws_kms_key.cluster-kms.key_id
}
