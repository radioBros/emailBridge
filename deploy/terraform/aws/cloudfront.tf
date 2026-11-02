# Optional CloudFront for a separately hosted admin SPA (S3 origin).
# The EmailBridge binary already embeds the SPA; enable only when you publish web/dist to S3.

resource "aws_cloudfront_origin_access_control" "admin" {
  count                             = var.cloudfront_admin ? 1 : 0
  name                              = "${local.name_prefix}-admin-oac"
  description                       = "EmailBridge admin SPA"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "admin" {
  count           = var.cloudfront_admin ? 1 : 0
  enabled         = true
  is_ipv6_enabled = true
  comment         = "EmailBridge admin SPA"
  price_class     = "PriceClass_100"

  origin {
    domain_name              = aws_s3_bucket.admin[0].bucket_regional_domain_name
    origin_id                = "admin-s3"
    origin_access_control_id = aws_cloudfront_origin_access_control.admin[0].id
  }

  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "admin-s3"
    viewer_protocol_policy = "redirect-to-https"
    compress               = true

    forwarded_values {
      query_string = false
      cookies {
        forward = "none"
      }
    }
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}

data "aws_iam_policy_document" "admin_bucket" {
  count = var.cloudfront_admin ? 1 : 0

  statement {
    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.admin[0].arn}/*"]
    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.admin[0].arn]
    }
  }
}

resource "aws_s3_bucket_policy" "admin" {
  count  = var.cloudfront_admin ? 1 : 0
  bucket = aws_s3_bucket.admin[0].id
  policy = data.aws_iam_policy_document.admin_bucket[0].json
}
