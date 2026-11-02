locals {
  name_prefix = "${var.name}-${var.environment}"

  s3_bucket = coalesce(var.s3_bucket_name, "${local.name_prefix}-data")

  nats_url = var.create_nats ? "nats://${aws_service_discovery_service.nats[0].name}.${aws_service_discovery_private_dns_namespace.main.name}:4222" : var.nats_url

  roles = {
    api = {
      roles          = "api"
      desired_count  = var.desired_counts.api
      cpu            = var.api_cpu
      memory         = var.api_memory
      container_port = 8080
    }
    sync = {
      roles          = "sync"
      desired_count  = var.desired_counts.sync
      cpu            = var.task_cpu
      memory         = var.task_memory
      container_port = 8080
    }
    webhook = {
      roles          = "webhook"
      desired_count  = var.desired_counts.webhook
      cpu            = var.task_cpu
      memory         = var.task_memory
      container_port = 8080
    }
    worker = {
      roles          = "submit,scheduler"
      desired_count  = var.desired_counts.worker
      cpu            = var.task_cpu
      memory         = var.task_memory
      container_port = 8080
    }
  }
}
