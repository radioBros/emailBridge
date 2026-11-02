# EmailBridge — Terraform (AWS ECS/Fargate)

Provisions ALB + ACM + Route53, ECS Fargate services per role (`api`, `sync`, `webhook`, `worker`),
S3, Secrets Manager wiring, CloudWatch logs, optional CloudFront admin SPA, optional self-run NATS.

**MongoDB is not created here.** Pass `mongo_uri_secret_arn` for Atlas (recommended) or a
DocumentDB-compatible replica-set URI. Operations guide: `docs/ops/large.md`.

## Usage

```bash
cp terraform.tfvars.example terraform.tfvars
# edit tfvars — never commit secrets

docker run --rm -v "$PWD:/work" -w /work/deploy/terraform/aws \
  -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN -e AWS_REGION \
  hashicorp/terraform:1.9.8 init

docker run --rm -v "$PWD:/work" -w /work/deploy/terraform/aws \
  hashicorp/terraform:1.9.8 fmt -check

docker run --rm -v "$PWD:/work" -w /work/deploy/terraform/aws \
  hashicorp/terraform:1.9.8 validate
```

Run `terraform apply` yourself, from a trusted workstation or a reviewed pipeline step. Do not let an
unattended CI job apply changes.

## Roles

| Service | `EB_ROLES` | Autoscaling |
| --- | --- | --- |
| api | `api` | CPU target tracking |
| sync | `sync` | fixed desired count |
| webhook | `webhook` | CPU target tracking |
| worker | `submit,scheduler` | fixed desired count |

`stopTimeout` / ALB deregistration = 75 s. No ECS init containers — the binary entrypoint migrates under
the lock.

## NATS

- `create_nats=true` (default): single JetStream task + Cloud Map DNS. Ephemeral disk — fine for
  smoke / staging; add EFS or use an external cluster for production.
- `create_nats=false` + `nats_url=…`: external NATS (recommended for HA; the tasks then run with
  `EB_NATS_REPLICAS=3`).

## S3 Object Lock

`s3_object_lock_enabled=true` turns on bucket Object Lock (governance, 30d default) for PEC
evidence archiving. Cannot be turned off after create — leave false unless you need it.
