resource "aws_ecs_cluster" "main" {
  name = local.name_prefix

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_ecs_task_definition" "role" {
  for_each                 = local.roles
  family                   = "${local.name_prefix}-${each.key}"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = each.value.cpu
  memory                   = each.value.memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([
    {
      name      = "emailbridge"
      image     = var.image
      essential = true
      command   = ["serve"]
      portMappings = [
        {
          containerPort = 8080
          protocol      = "tcp"
        },
        {
          containerPort = 9090
          protocol      = "tcp"
        }
      ]
      environment = [
        { name = "EB_ROLES", value = each.value.roles },
        { name = "EB_ENV", value = var.environment },
        { name = "EB_PUBLIC_URL", value = var.public_url },
        { name = "EB_NATS_MODE", value = "external" },
        { name = "EB_NATS_URL", value = local.nats_url },
        { name = "EB_NATS_REPLICAS", value = var.create_nats ? "1" : "3" },
        { name = "EB_MONGO_DATABASE", value = "emailbridge" },
        { name = "EB_S3_ENDPOINT", value = "https://s3.${var.aws_region}.amazonaws.com" },
        { name = "EB_S3_REGION", value = var.aws_region },
        { name = "EB_S3_BUCKET", value = aws_s3_bucket.data.bucket },
        { name = "EB_S3_PATH_STYLE", value = "false" },
        { name = "EB_DATA_DIR", value = "/data" },
        { name = "EB_METRICS_ADDR", value = ":9090" },
        { name = "EB_SHUTDOWN_GRACE", value = "60s" },
        { name = "EB_KEYS_PROVIDER", value = "local" },
      ]
      secrets = concat(
        [
          {
            name      = "EB_MONGO_URI"
            valueFrom = var.mongo_uri_secret_arn
          }
        ],
        var.create_app_secrets ? [
          {
            name      = "EB_AUTH_SECRET"
            valueFrom = aws_secretsmanager_secret.auth[0].arn
          },
          {
            name      = "EB_KEYS_LOCAL"
            valueFrom = aws_secretsmanager_secret.keys[0].arn
          }
        ] : []
      )
      # No init containers — entrypoint migrates under the lock.
      stopTimeout = 75
      healthCheck = {
        # Distroless: no shell — same as Containerfile HEALTHCHECK.
        command     = ["CMD", "/emailbridge", "healthcheck"]
        interval    = 15
        timeout     = 5
        retries     = 5
        startPeriod = 60
      }
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.role[each.key].name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = each.key
        }
      }
      mountPoints = []
      volumesFrom = []
    }
  ])
}

resource "aws_ecs_service" "role" {
  for_each                           = local.roles
  name                               = each.key
  cluster                            = aws_ecs_cluster.main.id
  task_definition                    = aws_ecs_task_definition.role[each.key].arn
  desired_count                      = each.value.desired_count
  launch_type                        = "FARGATE"
  platform_version                   = "LATEST"
  enable_execute_command             = var.enable_execute_command
  deployment_minimum_healthy_percent = 50
  deployment_maximum_percent         = 200

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.ecs.id]
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = each.key == "api" ? [1] : []
    content {
      target_group_arn = aws_lb_target_group.api.arn
      container_name   = "emailbridge"
      container_port   = 8080
    }
  }

  lifecycle {
    ignore_changes = [desired_count]
  }

  depends_on = [aws_lb_listener.https]
}

resource "aws_appautoscaling_target" "api" {
  max_capacity       = 10
  min_capacity       = var.desired_counts.api
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.role["api"].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "api_cpu" {
  name               = "${local.name_prefix}-api-cpu"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.api.resource_id
  scalable_dimension = aws_appautoscaling_target.api.scalable_dimension
  service_namespace  = aws_appautoscaling_target.api.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value = 70
  }
}

resource "aws_appautoscaling_target" "webhook" {
  max_capacity       = 10
  min_capacity       = var.desired_counts.webhook
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.role["webhook"].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "webhook_cpu" {
  name               = "${local.name_prefix}-webhook-cpu"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.webhook.resource_id
  scalable_dimension = aws_appautoscaling_target.webhook.scalable_dimension
  service_namespace  = aws_appautoscaling_target.webhook.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value = 70
  }
}
