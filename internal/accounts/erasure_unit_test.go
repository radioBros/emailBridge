package accounts

import "testing"

func TestRegisterHandlersNilJobs(t *testing.T) {
	(&Service{}).RegisterHandlers() // no-op
}
