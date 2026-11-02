// Package emailbridgesdk is the EmailBridge Go SDK.
//
// Subpackages:
//   - client: thin HTTP wrapper (auth, retries, ProblemError, cursor/SSE) matching the TS SDK
//   - openapi: oapi-codegen models + ClientWithResponses from api/openapi/openapi.yaml
//   - webhook: Standard Webhooks + bodyOnly verification
//   - events: CloudEvents envelope types (S3 §1)
package emailbridgesdk
