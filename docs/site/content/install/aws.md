# AWS (Terraform ECS/Fargate)

Module: `deploy/terraform/aws`. ALB + ACM + Route53, ECS services per role, S3, Secrets Manager,
CloudWatch logs; optional CloudFront admin SPA; optional self-run NATS on ECS.

**MongoDB is external** — pass `mongo_uri_secret_arn` (Atlas recommended; DocumentDB only if replica-set /
transactions capable).

```bash
cp deploy/terraform/aws/terraform.tfvars.example deploy/terraform/aws/terraform.tfvars
# edit; never commit secrets
docker run --rm -v "$PWD:/work" -w /work/deploy/terraform/aws \
  hashicorp/terraform:1.9.8 init
docker run --rm -v "$PWD:/work" -w /work/deploy/terraform/aws \
  hashicorp/terraform:1.9.8 plan
```

Review the plan and run `terraform apply` yourself; do not apply from an unattended CI job. README:
`deploy/terraform/aws/README.md`.
