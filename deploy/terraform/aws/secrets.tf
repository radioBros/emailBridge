resource "aws_secretsmanager_secret" "auth" {
  count       = var.create_app_secrets ? 1 : 0
  name        = "${local.name_prefix}/auth_secret"
  description = "EB_AUTH_SECRET (32-byte base64). Replace placeholder before first run."
}

resource "aws_secretsmanager_secret_version" "auth" {
  count         = var.create_app_secrets ? 1 : 0
  secret_id     = aws_secretsmanager_secret.auth[0].id
  secret_string = "REPLACE_ME_before_first_run"
}

resource "aws_secretsmanager_secret" "keys" {
  count       = var.create_app_secrets ? 1 : 0
  name        = "${local.name_prefix}/keys"
  description = "EB_KEYS_LOCAL key ring. Replace placeholder before first run."
}

resource "aws_secretsmanager_secret_version" "keys" {
  count         = var.create_app_secrets ? 1 : 0
  secret_id     = aws_secretsmanager_secret.keys[0].id
  secret_string = "REPLACE_ME_before_first_run"
}
