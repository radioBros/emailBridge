# Amazon SES

Send outbound mail through Amazon SES API v2 (SigV4). Delivery notifications (SNS) map onto outbound states (sent, ounced, complained, ailed).

**Data leaving EmailBridge:** full message content and envelope recipients to AWS.

Switch: `integrations.ses` (organisation, default off). Uninstall clears owned settings only.
