// Package sinks delivers CloudEvents to non-HTTP webhook endpoint kinds (S7 §6):
// NATS, Kafka, SQS, SNS and Google Pub/Sub. Callers pass already-decrypted sinkConfig;
// credentials are never logged. Every network dial goes through internal/egress when the
// client library accepts a custom dialer.
//
// Ambient cloud credentials (AWS default chain, Google ADC) are off by default via
// Deps.AllowAmbientCredentials. Claude wires that flag from the instance-only setting
// sinks.ambientCredentials (default false) so tenants cannot publish using the
// server's instance role.
package sinks
