resource "aws_iam_role" "secrets_manager_pod_role" {
  name = "${var.project_name}-${var.environment}-secrets-manager-pod-role"

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
  name = "${var.project_name}-${var.environment}-secrets-manager-read"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret",
          "secretsmanager:ListSecretVersionIds"
        ]

        Resource = compact([
          var.db_credentials_secret_arn,
          var.db_connection_secret_arn,
          var.grafana_secret_arn,
          var.jwt_secret_arn,
          var.alertmanager_slack_secret_arn
        ])
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

# order service sqs

resource "aws_iam_role" "order_service_sqs_pod_role" {
  name = "${var.project_name}-${var.environment}-order-service-sqs-pod-role"

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

resource "aws_iam_policy" "order_service_sqs" {
  name = "${var.project_name}-${var.environment}-order-service-sqs"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "sqs:SendMessage"
        ]

        Resource = var.sqs_queue_arn
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "order_service_sqs_attachment" {
  role       = aws_iam_role.order_service_sqs_pod_role.name
  policy_arn = aws_iam_policy.order_service_sqs.arn
}

resource "aws_eks_pod_identity_association" "order_service_sqs" {
  cluster_name    = var.cluster_name
  namespace       = var.order_service_namespace
  service_account = var.order_service_service_account
  role_arn        = aws_iam_role.order_service_sqs_pod_role.arn
}

# worker service sqs

resource "aws_iam_role" "worker_sqs_pod_role" {
  name = "${var.project_name}-${var.environment}-worker-sqs-pod-role"

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

resource "aws_iam_policy" "worker_sqs" {
  name = "${var.project_name}-${var.environment}-worker-sqs"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "sqs:ReceiveMessage",
          "sqs:DeleteMessage"
        ]

        Resource = var.sqs_queue_arn
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "worker_sqs_attachment" {
  role       = aws_iam_role.worker_sqs_pod_role.name
  policy_arn = aws_iam_policy.worker_sqs.arn
}

resource "aws_eks_pod_identity_association" "worker_sqs" {
  cluster_name    = var.cluster_name
  namespace       = var.worker_namespace
  service_account = var.worker_service_account
  role_arn        = aws_iam_role.worker_sqs_pod_role.arn
}

# external dns

resource "aws_iam_role" "external_dns_pod_role" {
  name = "${var.project_name}-${var.environment}-external-dns-pod-role"

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
  name = "${var.project_name}-${var.environment}-external-dns"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Effect = "Allow"

        Action = [
          "route53:ChangeResourceRecordSets",
          "route53:ListResourceRecordSets",
          "route53:ListTagsForResources"
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
  name = "${var.project_name}-${var.environment}-cert-manager-pod-role"

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
  name = "${var.project_name}-${var.environment}-cert-manager"

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

# karpenter

resource "aws_iam_role" "karpenter_pod_role" {
  name = "${var.project_name}-${var.environment}-karpenter-pod-role"

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

data "aws_caller_identity" "current" {}

resource "aws_iam_policy" "karpenter_policy" {
  name = "${var.project_name}-${var.environment}-karpenter-controller"

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [
      {
        Sid    = "AllowRegionalReadActions"
        Effect = "Allow"

        Action = [
          "ec2:DescribeCapacityReservations",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeImages",
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceTypeOfferings",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeLaunchTemplates",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeInstanceStatus",
          "ec2:DescribeSpotPriceHistory",
          "ec2:DescribeSubnets",
          "ec2:DescribePlacementGroups"
        ]

        Resource = "*"

        Condition = {
          StringEquals = {
            "aws:RequestedRegion" = var.aws_region
          }
        }
      },
      {
        Sid    = "AllowScopedEC2InstanceAccessActions"
        Effect = "Allow"

        Action = [
          "ec2:RunInstances",
          "ec2:CreateFleet"
        ]

        Resource = [
          "arn:aws:ec2:${var.aws_region}::image/*",
          "arn:aws:ec2:${var.aws_region}::snapshot/*",
          "arn:aws:ec2:${var.aws_region}:*:security-group/*",
          "arn:aws:ec2:${var.aws_region}:*:subnet/*",
          "arn:aws:ec2:${var.aws_region}:*:capacity-reservation/*",
          "arn:aws:ec2:${var.aws_region}:*:placement-group/*"
        ]
      },
      {
        Sid    = "AllowScopedEC2LaunchTemplateAccessActions"
        Effect = "Allow"

        Action = [
          "ec2:RunInstances",
          "ec2:CreateFleet"
        ]

        Resource = [
          "arn:aws:ec2:${var.aws_region}:*:launch-template/*"
        ]

        Condition = {
          StringEquals = {
            "aws:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
          }

          StringLike = {
            "aws:ResourceTag/karpenter.sh/nodepool" = "*"
          }
        }
      },
      {
        Sid    = "AllowScopedEC2InstanceActionsWithTags"
        Effect = "Allow"

        Action = [
          "ec2:RunInstances",
          "ec2:CreateFleet",
          "ec2:CreateLaunchTemplate"
        ]

        Resource = [
          "arn:aws:ec2:${var.aws_region}:*:fleet/*",
          "arn:aws:ec2:${var.aws_region}:*:instance/*",
          "arn:aws:ec2:${var.aws_region}:*:volume/*",
          "arn:aws:ec2:${var.aws_region}:*:network-interface/*",
          "arn:aws:ec2:${var.aws_region}:*:launch-template/*",
          "arn:aws:ec2:${var.aws_region}:*:spot-instances-request/*",
          "arn:aws:ec2:${var.aws_region}:*:capacity-reservation/*"
        ]

        Condition = {
          StringEquals = {
            "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
            "aws:RequestTag/eks:eks-cluster-name"                      = var.cluster_name
          }

          StringLike = {
            "aws:RequestTag/karpenter.sh/nodepool" = "*"
          }
        }
      },
      {
        Sid    = "AllowScopedResourceCreationTagging"
        Effect = "Allow"

        Action = [
          "ec2:CreateTags"
        ]

        Resource = [
          "arn:aws:ec2:${var.aws_region}:*:fleet/*",
          "arn:aws:ec2:${var.aws_region}:*:instance/*",
          "arn:aws:ec2:${var.aws_region}:*:volume/*",
          "arn:aws:ec2:${var.aws_region}:*:network-interface/*",
          "arn:aws:ec2:${var.aws_region}:*:launch-template/*",
          "arn:aws:ec2:${var.aws_region}:*:spot-instances-request/*"
        ]

        Condition = {
          StringEquals = {
            "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
            "aws:RequestTag/eks:eks-cluster-name"                      = var.cluster_name
            "ec2:CreateAction" = [
              "RunInstances",
              "CreateFleet",
              "CreateLaunchTemplate"
            ]
          }

          StringLike = {
            "aws:RequestTag/karpenter.sh/nodepool" = "*"
          }
        }
      },
      {
        Sid    = "AllowScopedResourceTagging"
        Effect = "Allow"

        Action = [
          "ec2:CreateTags"
        ]

        Resource = [
          "arn:aws:ec2:${var.aws_region}:*:instance/*"
        ]

        Condition = {
          StringEquals = {
            "aws:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
          }

          StringLike = {
            "aws:ResourceTag/karpenter.sh/nodepool" = "*"
          }

          StringEqualsIfExists = {
            "aws:RequestTag/eks:eks-cluster-name" = var.cluster_name
          }

          "ForAllValues:StringEquals" = {
            "aws:TagKeys" = [
              "eks:eks-cluster-name",
              "karpenter.sh/nodeclaim",
              "Name"
            ]
          }
        }
      },
      {
        Sid    = "AllowScopedDeletion"
        Effect = "Allow"

        Action = [
          "ec2:TerminateInstances",
          "ec2:DeleteLaunchTemplate"
        ]

        Resource = [
          "arn:aws:ec2:${var.aws_region}:*:instance/*",
          "arn:aws:ec2:${var.aws_region}:*:launch-template/*"
        ]

        Condition = {
          StringEquals = {
            "aws:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
          }

          StringLike = {
            "aws:ResourceTag/karpenter.sh/nodepool" = "*"
          }
        }
      },
      {
        Sid    = "AllowPassingInstanceRole"
        Effect = "Allow"

        Action = [
          "iam:PassRole"
        ]

        Resource = [
          var.worker_node_role_arn
        ]

        Condition = {
          StringEquals = {
            "iam:PassedToService" = "ec2.amazonaws.com"
          }
        }
      },
      {
        Sid    = "AllowSSMReadActions"
        Effect = "Allow"

        Action = [
          "ssm:GetParameter"
        ]

        Resource = [
          "arn:aws:ssm:${var.aws_region}::parameter/aws/service/*"
        ]
      },
      {
        Sid    = "AllowPricingReadActions"
        Effect = "Allow"

        Action = [
          "pricing:GetProducts"
        ]

        Resource = "*"
      },
      {
        Sid    = "AllowScopedInstanceProfileCreationActions"
        Effect = "Allow"

        Action = [
          "iam:CreateInstanceProfile"
        ]

        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/*"
        ]

        Condition = {
          StringEquals = {
            "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
            "aws:RequestTag/eks:eks-cluster-name"                      = var.cluster_name
            "aws:RequestTag/topology.kubernetes.io/region"             = var.aws_region
          }

          StringLike = {
            "aws:RequestTag/karpenter.k8s.aws/ec2nodeclass" = "*"
          }
        }
      },
      {
        Sid    = "AllowScopedInstanceProfileTagActions"
        Effect = "Allow"

        Action = [
          "iam:TagInstanceProfile"
        ]

        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/*"
        ]

        Condition = {
          StringEquals = {
            "aws:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
            "aws:ResourceTag/topology.kubernetes.io/region"             = var.aws_region
            "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}"  = "owned"
            "aws:RequestTag/eks:eks-cluster-name"                       = var.cluster_name
            "aws:RequestTag/topology.kubernetes.io/region"              = var.aws_region
          }

          StringLike = {
            "aws:ResourceTag/karpenter.k8s.aws/ec2nodeclass" = "*"
            "aws:RequestTag/karpenter.k8s.aws/ec2nodeclass"  = "*"
          }
        }
      },
      {
        Sid    = "AllowScopedInstanceProfileActions"
        Effect = "Allow"

        Action = [
          "iam:AddRoleToInstanceProfile",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:DeleteInstanceProfile"
        ]

        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/*"
        ]

        Condition = {
          StringEquals = {
            "aws:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
            "aws:ResourceTag/topology.kubernetes.io/region"             = var.aws_region
          }

          StringLike = {
            "aws:ResourceTag/karpenter.k8s.aws/ec2nodeclass" = "*"
          }
        }
      },
      {
        Sid    = "AllowInstanceProfileReadActions"
        Effect = "Allow"

        Action = [
          "iam:GetInstanceProfile"
        ]

        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/*"
        ]
      },
      {
        Sid    = "AllowUnscopedInstanceProfileListAction"
        Effect = "Allow"

        Action = [
          "iam:ListInstanceProfiles"
        ]

        Resource = "*"
      },
      {
        Sid    = "AllowAPIServerEndpointDiscovery"
        Effect = "Allow"

        Action = [
          "eks:DescribeCluster"
        ]

        Resource = [
          "arn:aws:eks:${var.aws_region}:${data.aws_caller_identity.current.account_id}:cluster/${var.cluster_name}"
        ]
      }
    ]
  })
}


resource "aws_iam_role_policy_attachment" "karpenter_attachment" {
  role       = aws_iam_role.karpenter_pod_role.name
  policy_arn = aws_iam_policy.karpenter_policy.arn
}

resource "aws_eks_pod_identity_association" "karpenter" {
  cluster_name    = var.cluster_name
  namespace       = "kube-system"
  service_account = "karpenter"
  role_arn        = aws_iam_role.karpenter_pod_role.arn
}

# aws load balancer controller

resource "aws_iam_role" "aws_load_balancer_controller_pod_role" {
  name = "${var.project_name}-${var.environment}-aws-load-balancer-controller-pod-role"

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

resource "aws_iam_policy" "aws_load_balancer_controller" {
  name   = "${var.project_name}-${var.environment}-aws-load-balancer-controller"
  policy = file("${path.module}/policies/aws-load-balancer-controller.json")
}

resource "aws_iam_role_policy_attachment" "aws_load_balancer_controller_attachment" {
  role       = aws_iam_role.aws_load_balancer_controller_pod_role.name
  policy_arn = aws_iam_policy.aws_load_balancer_controller.arn
}

resource "aws_eks_pod_identity_association" "aws_load_balancer_controller" {
  cluster_name    = var.cluster_name
  namespace       = "kube-system"
  service_account = "aws-load-balancer-controller"
  role_arn        = aws_iam_role.aws_load_balancer_controller_pod_role.arn
}
