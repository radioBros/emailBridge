//go:build integration || e2e

package containers

import "os"

// ImageMongo is the MongoDB test image. EB_TEST_MONGO_IMAGE overrides it: MongoDB 8 refuses to start on
// Linux kernels 6.19 to 7.0.13 (SERVER-121912), which CI runners use, so CI runs the tests on mongo:7.0.
var ImageMongo = func() string {
	if img := os.Getenv("EB_TEST_MONGO_IMAGE"); img != "" {
		return img
	}
	return "mongo:8"
}()

// Pinned image references.
const (
	ImageNATS    = "nats:2.11.7"
	ImageS3      = "chrislusf/seaweedfs:4.50"
	ImageDovecot = "dovecot/dovecot:2.4.5"
	ImageClamAV  = "clamav/clamav:1.4.3" // P4.3 antivirus integration tests
)
