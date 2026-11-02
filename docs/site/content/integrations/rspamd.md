# Rspamd

Score inbound mail with Rspamd `/checkv2`. When enabled, raw MIME is sent to your Rspamd instance; `message.received.spam` carries score, action and symbols.

**Data leaving EmailBridge:** raw message bytes to Rspamd.

Switch: `integrations.rspamd`.

## How scoring runs

- Turn on the `integrations.rspamd` switch (organisation or per account). Sync nodes then send the first 4 MiB of
  every **new** inbound message to `<baseUrl>/checkv2` and add `spam: {score, action, symbols}` to
  `message.received`. A scoring failure never blocks sync: the event is sent without a score.
- The instance is looked up once a minute per node, so an edit applies within a minute.
- Rspamd usually runs on a private network (`http://rspamd:11333`). EmailBridge connects through its egress
  dialer, which refuses private addresses by default: add the Rspamd host to the egress allow-list
  (`EB_EGRESS_*`, see the egress docs) or scoring is refused.
- The controller password, when set, is stored encrypted and sent as the `Password` header.
