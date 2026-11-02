package integrations

import "github.com/radiobros/emailbridge/internal/integrations/connectors"

func slackDefinition() *Definition {
	return Materialize(connectors.Slack())
}
