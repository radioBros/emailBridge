variable "aws_region" {
  type        = string
  description = "Primary AWS region for ECS, ALB, S3, Secrets Manager."
  default     = "eu-south-1"
}

variable "name" {
  type        = string
  description = "Name prefix for resources."
  default     = "emailbridge"
}

variable "environment" {
  type        = string
  description = "EB_ENV value (development|staging|production)."
  default     = "production"
}

variable "vpc_id" {
  type        = string
  description = "Existing VPC id."
}

variable "private_subnet_ids" {
  type        = list(string)
  description = "Private subnets for ECS tasks."
}

variable "public_subnet_ids" {
  type        = list(string)
  description = "Public subnets for the ALB."
}

variable "public_url" {
  type        = string
  description = "External base URL (EB_PUBLIC_URL), e.g. https://mail-gw.example.com"
}

variable "domain_name" {
  type        = string
  description = "DNS name for the ALB (Route53 A/AAAA alias)."
}

variable "hosted_zone_id" {
  type        = string
  description = "Route53 hosted zone id for domain_name and ACM validation."
}

variable "image" {
  type        = string
  description = "Container image (signed release from GHCR)."
  default     = "ghcr.io/radiobros/emailbridge:v1.0.0"
}

variable "mongo_uri_secret_arn" {
  type        = string
  description = "Secrets Manager ARN whose secret string is the MongoDB URI (Atlas or DocumentDB-compatible)."
}

variable "create_app_secrets" {
  type        = bool
  description = "Create placeholder Secrets Manager secrets for auth_secret and keys (fill before first run)."
  default     = true
}

variable "nats_url" {
  type        = string
  description = "Comma-separated NATS URLs. Empty + create_nats=true uses the in-module ECS NATS service."
  default     = ""
}

variable "create_nats" {
  type        = bool
  description = "Run a small JetStream NATS service on ECS (self-run). Prefer an external cluster for production HA."
  default     = true
}

variable "s3_bucket_name" {
  type        = string
  description = "S3 bucket name for EmailBridge object storage. Empty = name-environment-data prefix."
  default     = ""
}

variable "s3_object_lock_enabled" {
  type        = bool
  description = "Enable S3 Object Lock (governance) for PEC evidence archiving. Cannot be disabled after create."
  default     = false
}

variable "cloudfront_admin" {
  type        = bool
  description = "Optional CloudFront + S3 for a separately hosted admin SPA (binary already embeds SPA)."
  default     = false
}

variable "desired_counts" {
  type = object({
    api     = number
    sync    = number
    webhook = number
    worker  = number
  })
  default = {
    api     = 2
    sync    = 2
    webhook = 2
    worker  = 2
  }
}

variable "api_cpu" {
  type    = number
  default = 512
}

variable "api_memory" {
  type    = number
  default = 1024
}

variable "task_cpu" {
  type        = number
  description = "CPU for sync/webhook/worker tasks."
  default     = 512
}

variable "task_memory" {
  type        = number
  description = "Memory (MiB) for sync/webhook/worker tasks."
  default     = 1024
}

variable "enable_execute_command" {
  type        = bool
  description = "ECS Exec (debug). Off by default."
  default     = false
}

variable "tags" {
  type        = map(string)
  description = "Extra resource tags."
  default     = {}
}
