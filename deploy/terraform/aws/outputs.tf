output "alb_dns_name" {
  value       = aws_lb.api.dns_name
  description = "ALB DNS name."
}

output "api_url" {
  value       = "https://${var.domain_name}"
  description = "Public API / admin URL."
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.main.name
}

output "s3_bucket" {
  value       = aws_s3_bucket.data.bucket
  description = "Object-storage bucket (EB_S3_BUCKET)."
}

output "nats_url" {
  value       = local.nats_url
  description = "NATS URL used by tasks."
}

output "cloudfront_admin_domain" {
  value       = try(aws_cloudfront_distribution.admin[0].domain_name, null)
  description = "CloudFront domain when cloudfront_admin=true."
}

output "mongo_note" {
  value = <<-EOT
    MongoDB is external. Point mongo_uri_secret_arn at an Atlas connection string
    (preferred) or a DocumentDB-compatible replica-set URI. EmailBridge requires
    transactions (replica set). DocumentDB works only when configured for that;
    Atlas is the recommended production choice.
  EOT
}
