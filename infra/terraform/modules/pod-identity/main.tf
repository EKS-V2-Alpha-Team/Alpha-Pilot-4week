resource "aws_iam_role" "secrets_manager_pod_role" {
  name = "${var.project_name}-secrets-manager-pod-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Principal = {
          Service = "pods.eks.amazonaws.com"
        }

        Action = [
          "sts:AssumeRole",
          "sts:TagSession"
        ]
      }
    ]
  })
}

resource "aws_iam_policy" "secrets_manager_read" {
  name = "${var.project_name}-secrets-manager-read"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "secretsmanager:GetSecretValue"
        ]

        Resource = var.db_secret_arn
      },
      {
        Effect = "Allow"

        Action = [
          "kms:Decrypt"
        ]

        Resource = var.kms_key_arn
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "secrets_manager_attachment" {
  role       = aws_iam_role.secrets_manager_pod_role.name
  policy_arn = aws_iam_policy.secrets_manager_read.arn
}

resource "aws_eks_pod_identity_association" "secrets_manager" {
  cluster_name    = var.cluster_name
  namespace       = var.secrets_manager_namespace
  service_account = var.secrets_manager_service_account
  role_arn        = aws_iam_role.secrets_manager_pod_role.arn
}

# SQS pod identity

resource "aws_iam_role" "sqs_pod_role" {
  name = "${var.project_name}-sqs-pod-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Principal = {
          Service = "pods.eks.amazonaws.com"
        }

        Action = [
          "sts:AssumeRole",
          "sts:TagSession"
        ]
      }
    ]
  })
}

resource "aws_iam_policy" "sqs_access" {
  name = "${var.project_name}-sqs-access"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "sqs:SendMessage",
          "sqs:ReceiveMessage",
          "sqs:DeleteMessage",
          "sqs:ChangeMessageVisibility"
        ]

        Resource = var.sqs_queue_arn
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "sqs_attachment" {
  role       = aws_iam_role.sqs_pod_role.name
  policy_arn = aws_iam_policy.sqs_access.arn
}

resource "aws_eks_pod_identity_association" "sqs" {
  cluster_name    = var.cluster_name
  namespace       = var.sqs_namespace
  service_account = var.sqs_service_account
  role_arn        = aws_iam_role.sqs_pod_role.arn
}

# external-dns

resource "aws_iam_role" "external_dns_pod_role" {
  name = "${var.project_name}-external-dns-pod-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Principal = {
          Service = "pods.eks.amazonaws.com"
        }

        Action = [
          "sts:AssumeRole",
          "sts:TagSession"
        ]
      }
    ]
  })
}

resource "aws_iam_policy" "external_dns" {
  name = "${var.project_name}-external-dns"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "route53:ChangeResourceRecordSets",
          "route53:ListResourceRecordSets"
        ]

        Resource = var.route53_hosted_zone_arn
      },
      {
        Effect = "Allow"

        Action = [
          "route53:ListHostedZones",
          "route53:ListHostedZonesByName"
        ]

        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "external_dns_attachment" {
  role       = aws_iam_role.external_dns_pod_role.name
  policy_arn = aws_iam_policy.external_dns.arn
}

resource "aws_eks_pod_identity_association" "external_dns" {
  cluster_name    = var.cluster_name
  namespace       = "external-dns" # no variable needed as this is the namespace/svc account ,that external dns deploys into the cluster
  service_account = "external-dns"
  role_arn        = aws_iam_role.external_dns_pod_role.arn
}

# cert manager

resource "aws_iam_role" "cert_manager_pod_role" {
  name = "${var.project_name}-cert-manager-pod-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Principal = {
          Service = "pods.eks.amazonaws.com"
        }

        Action = [
          "sts:AssumeRole",
          "sts:TagSession"
        ]
      }
    ]
  })
}

resource "aws_iam_policy" "cert_manager" {
  name = "${var.project_name}-cert-manager"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "route53:GetChange"
        ]

        Resource = "arn:aws:route53:::change/*"
      },
      {
        Effect = "Allow"

        Action = [
          "route53:ChangeResourceRecordSets",
          "route53:ListResourceRecordSets"
        ]

        Resource = var.route53_hosted_zone_arn
      },
      {
        Effect = "Allow"

        Action = [
          "route53:ListHostedZonesByName"
        ]

        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "cert_manager_attachment" {
  role       = aws_iam_role.cert_manager_pod_role.name
  policy_arn = aws_iam_policy.cert_manager.arn
}

resource "aws_eks_pod_identity_association" "cert_manager" {
  cluster_name    = var.cluster_name
  namespace       = "cert-manager" # no variable needed as this is the namespace/svc account ,that cert manager deploys into the cluster but double check for cert manager
  service_account = "cert-manager"
  role_arn        = aws_iam_role.cert_manager_pod_role.arn
}
