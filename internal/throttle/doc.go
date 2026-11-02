// Package throttle enforces per-domain and per-provider send rate limits, time-zone send windows,
// and an optional CEL pre-send policy (P5.5, docs/spec/P5.5-throttle.md). Cluster counters use a
// Counter (typically bus.CacheAdd on eb-cache). CEL evaluation is provided by rules.PreSendEngine.
package throttle
