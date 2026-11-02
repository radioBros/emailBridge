//go:build integration || e2e

package containers

// Pinned image references (tags now; digests later). S14 §2.1.
const (
	ImageMongo   = "mongo:8"
	ImageNATS    = "nats:2.11.7"
	ImageS3      = "chrislusf/seaweedfs:4.50"
	ImageDovecot = "dovecot/dovecot:2.4.5"
	ImageClamAV  = "clamav/clamav:1.4.3" // P4.3 antivirus integration tests
)
