resource "aws_s3_bucket" "terraform_state" {
  bucket        = var.state_bucket_name
  force_destroy = var.environment != "prod"

  tags = {
    Name = "${var.project_name}-tf-state"
  }
}

resource "aws_s3_bucket_policy" "terraform_state_tls" {
  bucket     = aws_s3_bucket.terraform_state.id
  depends_on = [aws_s3_bucket_public_access_block.terraform_state]

  policy = templatefile("${path.module}/policies/s3-tls-deny.json", {
    bucket_arn = aws_s3_bucket.terraform_state.arn
  })
}

resource "aws_s3_bucket_versioning" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  rule {
    id     = "cleanup-old-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 90
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

resource "aws_ecr_repository" "main" {
  name                 = "${var.project_name}-shared-ecr"
  image_tag_mutability = "IMMUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = {
    Name = "${var.project_name}-shared-ecr"
  }
}

resource "aws_ecr_lifecycle_policy" "main" {
  repository = aws_ecr_repository.main.name
  policy     = file("${path.module}/policies/ecr-lifecycle.json")
}

resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]

  tags = {
    Name = "${var.project_name}-github-oidc-provider"
  }
}

# new state

locals {
  state_buckets = toset(["nonprod", "prod"])
}

resource "aws_s3_bucket" "tf_state" {
  for_each = local.state_buckets

  bucket        = "${var.project_name}-${each.key}-tfstate"
  force_destroy = false

  #lifecycle {
  #  prevent_destroy = true
  #}

  tags = {
    Name        = "${var.project_name}-${each.key}-tf-state"
    Environment = each.key
  }
}

resource "aws_s3_bucket_public_access_block" "tf_state" {
  for_each = local.state_buckets
  bucket   = aws_s3_bucket.tf_state[each.key].id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_policy" "tf_state_tls" {
  for_each   = local.state_buckets
  bucket     = aws_s3_bucket.tf_state[each.key].id
  depends_on = [aws_s3_bucket_public_access_block.tf_state]

  policy = templatefile("${path.module}/policies/s3-tls-deny.json", {
    bucket_arn = aws_s3_bucket.tf_state[each.key].arn
  })
}

resource "aws_s3_bucket_versioning" "tf_state" {
  for_each = local.state_buckets
  bucket   = aws_s3_bucket.tf_state[each.key].id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tf_state" {
  for_each = local.state_buckets
  bucket   = aws_s3_bucket.tf_state[each.key].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_ownership_controls" "tf_state" {
  for_each = local.state_buckets
  bucket   = aws_s3_bucket.tf_state[each.key].id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "tf_state" {
  for_each = local.state_buckets
  bucket   = aws_s3_bucket.tf_state[each.key].id

  rule {
    id     = "cleanup-old-versions"
    status = "Enabled"
    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 90
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}
