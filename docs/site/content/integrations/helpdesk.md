# Generic helpdesk

Recipe connector: one webhook URL plus a CEL filter and JSON `map`. Use it for any helpdesk that
accepts HTTP JSON.

## Setup

1. Admin → Integrations → Generic helpdesk → Install with `webhookUrl`.
2. After install, edit the route filter/map (or set them at install) to match your provider.

## What leaves EmailBridge

Whatever fields your `map` includes (default: event type, subject, from, account id).

Switch: `integrations.helpdesk`.
