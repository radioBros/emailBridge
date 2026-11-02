# Optional self-run NATS JetStream on ECS (single task). For production HA use an external
# 3-node cluster and set create_nats=false + nats_url. Ephemeral storage — not for durable
# production without an EFS volume (TODO: optional EFS).

resource "aws_service_discovery_service" "nats" {
  count = var.create_nats ? 1 : 0
  name  = "nats"

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.main.id
    dns_records {
      ttl  = 10
      type = "A"
    }
    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
    failure_threshold = 1
  }
}

resource "aws_ecs_task_definition" "nats" {
  count                    = var.create_nats ? 1 : 0
  family                   = "${local.name_prefix}-nats"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 256
  memory                   = 512
  execution_role_arn       = aws_iam_role.execution.arn

  container_definitions = jsonencode([
    {
      name      = "nats"
      image     = "nats:2.15.1-alpine"
      essential = true
      command   = ["-js", "-m", "8222", "--store_dir", "/data"]
      portMappings = [
        { containerPort = 4222, protocol = "tcp" },
        { containerPort = 8222, protocol = "tcp" }
      ]
      healthCheck = {
        command     = ["CMD", "wget", "-qO-", "http://127.0.0.1:8222/healthz"]
        interval    = 10
        timeout     = 5
        retries     = 5
        startPeriod = 20
      }
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.nats[0].name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "nats"
        }
      }
    }
  ])
}

resource "aws_ecs_service" "nats" {
  count           = var.create_nats ? 1 : 0
  name            = "nats"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.nats[0].arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.nats[0].id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.nats[0].arn
  }
}

check "nats_url_or_create" {
  assert {
    condition     = var.create_nats || length(var.nats_url) > 0
    error_message = "Set create_nats=true or provide nats_url for an external NATS cluster."
  }
}
