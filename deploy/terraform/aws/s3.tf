resource "aws_s3_bucket" "data" {
  bucket              = local.s3_bucket
  object_lock_enabled = var.s3_object_lock_enabled
}

resource "aws_s3_bucket_versioning" "data" {
  bucket = aws_s3_bucket.data.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "data" {
  bucket = aws_s3_bucket.data.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "data" {
  bucket                  = aws_s3_bucket.data.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_object_lock_configuration" "data" {
  count  = var.s3_object_lock_enabled ? 1 : 0
  bucket = aws_s3_bucket.data.id

  rule {
    default_retention {
      mode = "GOVERNANCE"
      days = 30
    }
  }
}

# Optional SPA origin (admin assets). Binary already embeds SPA; use when serving web/dist separately.
resource "aws_s3_bucket" "admin" {
  count  = var.cloudfront_admin ? 1 : 0
  bucket = "${local.name_prefix}-admin"
}

resource "aws_s3_bucket_public_access_block" "admin" {
  count                   = var.cloudfront_admin ? 1 : 0
  bucket                  = aws_s3_bucket.admin[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
