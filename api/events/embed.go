// Package eventschemas embeds the generated event JSON Schemas (S3 §2) so every instance serves them at
// /schemas/events/{type}/v{N}.json. Regenerate with `go run ./tools/eventschema`; never edit by hand.
package eventschemas

import "embed"

// FS holds <type>.v<N>.json and envelope.json.
//
//go:embed *.json
var FS embed.FS
