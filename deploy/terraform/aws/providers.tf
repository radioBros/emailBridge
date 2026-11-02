provider "aws" {
  region = var.aws_region

  default_tags {
    tags = merge(
      {
        Project   = "emailbridge"
        ManagedBy = "terraform"
      },
      var.tags,
    )
  }
}

# Optional alias for CloudFront ACM (must be us-east-1).
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"

  default_tags {
    tags = merge(
      {
        Project   = "emailbridge"
        ManagedBy = "terraform"
      },
      var.tags,
    )
  }
}
