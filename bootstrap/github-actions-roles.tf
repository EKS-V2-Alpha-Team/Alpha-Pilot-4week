locals {
  github_sub_prefix = "repo:${var.github_org}@${var.github_org_id}/${var.github_repo}@${var.github_repo_id}"
  environments      = toset(["dev", "staging", "prod"])

  environment_subjects = {
    for env in local.environments : env => (
      env == "dev" && var.legacy_repo_wide_trust
      ? ["${local.github_sub_prefix}:*"]
      : ["${local.github_sub_prefix}:environment:${env}"]
    )
  }

  no_environment_subjects = [
    "${local.github_sub_prefix}:pull_request",
    "${local.github_sub_prefix}:ref:refs/heads/main",
  ]
}

resource "aws_iam_role" "github_actions" {
  for_each = local.environments

  name = "${var.project_name}-${each.key}-github-actions-role"

  assume_role_policy = templatefile("${path.module}/policies/github-actions-trust.json", {
    oidc_provider_arn = aws_iam_openid_connect_provider.github.arn
    subjects = (
      length(local.environment_subjects[each.key]) == 1
      ? jsonencode(local.environment_subjects[each.key][0])
      : jsonencode(local.environment_subjects[each.key])
    )
  })

  tags = each.key == "dev" ? tomap({
    Name = "${var.project_name}-github-actions-role"
    }) : tomap({
    Name        = "${var.project_name}-${each.key}-github-actions-role"
    Environment = each.key
  })
}

resource "aws_iam_role_policy" "github_actions" {
  for_each = local.environments

  name   = "${var.project_name}-${each.key}-github-actions-policy"
  role   = aws_iam_role.github_actions[each.key].id
  policy = file("${path.module}/policies/github-actions-permissions.json")
}

moved {
  from = aws_iam_role.github_actions
  to   = aws_iam_role.github_actions["dev"]
}

moved {
  from = aws_iam_role_policy.github_actions
  to   = aws_iam_role_policy.github_actions["dev"]
}

resource "aws_iam_role" "github_actions_plan" {
  name = "${var.project_name}-github-actions-plan-role"

  assume_role_policy = templatefile("${path.module}/policies/github-actions-trust.json", {
    oidc_provider_arn = aws_iam_openid_connect_provider.github.arn
    subjects          = jsonencode(local.no_environment_subjects)
  })

  tags = {
    Name = "${var.project_name}-github-actions-plan-role"
  }
}

resource "aws_iam_role_policy_attachment" "github_actions_plan_read_only" {
  role       = aws_iam_role.github_actions_plan.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

resource "aws_iam_role_policy" "github_actions_plan" {
  name = "${var.project_name}-github-actions-plan-policy"
  role = aws_iam_role.github_actions_plan.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "StateLockFiles"
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:DeleteObject"]
        Resource = [for b in aws_s3_bucket.tf_state : "${b.arn}/*.tflock"]
      },
      {
        Sid    = "ReadProjectSecrets"
        Effect = "Allow"
        Action = ["secretsmanager:GetSecretValue"]
        Resource = [
          "arn:aws:secretsmanager:${var.aws_region}:*:secret:${var.project_name}-*",
          "arn:aws:secretsmanager:${var.aws_region}:*:secret:rds!*",
        ]
      },
      {
        Sid      = "GeneratePasswords"
        Effect   = "Allow"
        Action   = ["secretsmanager:GetRandomPassword"]
        Resource = "*"
      },
      {
        Sid      = "DecryptSecretsThroughSecretsManager"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = "*"
        Condition = {
          StringEquals = { "kms:ViaService" = "secretsmanager.${var.aws_region}.amazonaws.com" }
        }
      },
    ]
  })
}

resource "aws_iam_role" "github_actions_ecr" {
  name = "${var.project_name}-github-actions-ecr-role"

  assume_role_policy = templatefile("${path.module}/policies/github-actions-trust.json", {
    oidc_provider_arn = aws_iam_openid_connect_provider.github.arn
    subjects          = jsonencode(local.no_environment_subjects)
  })

  tags = {
    Name = "${var.project_name}-github-actions-ecr-role"
  }
}

resource "aws_iam_role_policy" "github_actions_ecr" {
  name = "${var.project_name}-github-actions-ecr-policy"
  role = aws_iam_role.github_actions_ecr.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "EcrLogin"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid      = "EcrPublicLogin"
        Effect   = "Allow"
        Action   = ["ecr-public:GetAuthorizationToken", "sts:GetServiceBearerToken"]
        Resource = "*"
      },
      {
        Sid    = "PushAndInspectImages"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:CompleteLayerUpload",
          "ecr:DescribeImages",
          "ecr:GetDownloadUrlForLayer",
          "ecr:InitiateLayerUpload",
          "ecr:PutImage",
          "ecr:UploadLayerPart",
        ]
        Resource = aws_ecr_repository.main.arn
      },
    ]
  })
}
