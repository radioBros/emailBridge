resource "aws_cloudwatch_log_group" "role" {
  for_each          = local.roles
  name              = "/ecs/${local.name_prefix}/${each.key}"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "nats" {
  count             = var.create_nats ? 1 : 0
  name              = "/ecs/${local.name_prefix}/nats"
  retention_in_days = 14
}
