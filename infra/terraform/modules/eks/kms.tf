resource "aws_kms_key" "eks" {
  description         = "Envelope encryption key for ${var.project_name} EKS secrets"
  enable_key_rotation = true
}

resource "aws_kms_alias" "eks" {
  name          = "alias/${var.project_name}-eks-secrets"
  target_key_id = aws_kms_key.eks.key_id
}
