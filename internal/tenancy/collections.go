package tenancy

// ErasureClass says how a MongoDB collection is covered by GDPR erasure (W34 / S11 §5).
type ErasureClass string

const (
	// EraseByOrganization — wiped (or specially handled) during organization.erasure.
	EraseByOrganization ErasureClass = "organization"
	// EraseByAccount — wiped during account erasure (by accountId or _id = account id).
	EraseByAccount ErasureClass = "account"
	// EraseBoth — account erase by account key; org erase also deletes residual by organizationId.
	EraseBoth ErasureClass = "both"
	// EraseExcluded — not erased here; Reason is required.
	EraseExcluded ErasureClass = "excluded"
)

// AccountEraseKey is the Mongo filter field used for account erasure.
type AccountEraseKey string

const (
	AccountKeyAccountID AccountEraseKey = "accountId"
	AccountKeyID        AccountEraseKey = "_id"
)

// CollectionEntry is one MongoDB collection in the anti-drift registry.
type CollectionEntry struct {
	Name       string
	Class      ErasureClass
	Reason     string          // required when Class == EraseExcluded
	AccountKey AccountEraseKey // for account / both; empty → accountId
}

// CollectionRegistry lists every MongoDB collection name used under internal/** exactly once.
var CollectionRegistry = []CollectionEntry{
	// --- organisation ---
	{Name: "organizations", Class: EraseByOrganization, Reason: "final org document delete"},
	{Name: "memberships", Class: EraseByOrganization},
	{Name: "webhookEndpoints", Class: EraseByOrganization},
	{Name: "routes", Class: EraseByOrganization},
	{Name: "routeVersions", Class: EraseByOrganization},
	{Name: "ruleErrors", Class: EraseByOrganization},
	{Name: "ruleLists", Class: EraseByOrganization},
	{Name: "ruleStats", Class: EraseByOrganization},
	{Name: "ruleDecisions", Class: EraseByOrganization},
	{Name: "endpointRuleVersions", Class: EraseByOrganization},
	{Name: "templates", Class: EraseByOrganization},
	{Name: "templateVersions", Class: EraseByOrganization},
	{Name: "smtpGateways", Class: EraseByOrganization},
	{Name: "suppressionLists", Class: EraseByOrganization},
	{Name: "suppressionEntries", Class: EraseByOrganization},
	{Name: "integrationInstances", Class: EraseByOrganization},
	{Name: "apiKeys", Class: EraseByOrganization},
	{Name: "aiConfigs", Class: EraseByOrganization},
	{Name: "oauthApps", Class: EraseByOrganization},
	{Name: "brandingAssets", Class: EraseByOrganization}, // W68; S3 prefix erased via connect.EraseOrgObjects
	{Name: "notifications", Class: EraseByOrganization},  // W75; org-scoped rows only (instance scope excluded by query)
	{Name: "notificationReads", Class: EraseByOrganization},
	// W75 per-user email records (90 d TTL); instance-scope rows have no organizationId and are kept.
	{Name: "notificationEmails", Class: EraseByOrganization},
	{Name: "apiCallStats", Class: EraseByOrganization}, // W74 hourly API-call counters (90 d TTL)
	{Name: "jobs", Class: EraseByOrganization, Reason: "finished org jobs; running erasure job kept"},
	{Name: "settings", Class: EraseByOrganization, Reason: "org:<id> doc only; never global/queues"},
	{Name: "dataKeys", Class: EraseByOrganization},

	// --- account ---
	{Name: "accounts", Class: EraseByAccount, Reason: "final account document delete"},
	{Name: "messages", Class: EraseByAccount, AccountKey: AccountKeyAccountID},
	{Name: "archivedMessages", Class: EraseByAccount, AccountKey: AccountKeyAccountID},
	{Name: "mailboxes", Class: EraseByAccount, AccountKey: AccountKeyAccountID},
	{Name: "outboundMessages", Class: EraseByAccount, AccountKey: AccountKeyAccountID},
	{Name: "attachments", Class: EraseByAccount, AccountKey: AccountKeyAccountID},
	// --- both (account key + org residual by organizationId) ---
	{Name: "msaCredentials", Class: EraseBoth, AccountKey: AccountKeyID},
	{Name: "imapProxyCredentials", Class: EraseBoth, AccountKey: AccountKeyID},
	{Name: "gmailSyncState", Class: EraseBoth, AccountKey: AccountKeyID},
	{Name: "graphSyncState", Class: EraseBoth, AccountKey: AccountKeyID},
	{Name: "events", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "idempotencyKeys", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "deliveries", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "capturedMessages", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "exports", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "drafts", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "deliveryTests", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "trackingLinks", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "trackingEvents", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	{Name: "connectLinks", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	// PEC receipt rows are erased; their locked evidence objects stay until retention ends (S17 §9, S11).
	{Name: "pecReceipts", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	// G3 full-text store: message text derived from the index, erased with the account and the organisation.
	{Name: "messageText", Class: EraseBoth, AccountKey: AccountKeyAccountID},
	// G1 semantic search vectors: chunks of message text with their embeddings, erased like messageText.
	{Name: "messageEmbeddings", Class: EraseBoth, AccountKey: AccountKeyAccountID},

	// --- excluded ---
	{Name: "users", Class: EraseExcluded, Reason: "erased by user deletion (S11 §5.3), not organisation"},
	{Name: "userSettings", Class: EraseExcluded, Reason: "keyed by userId; orphan cleanup on org erase when user has no memberships left"},
	{Name: "auditLog", Class: EraseExcluded, Reason: "scrubbed on org erase; retained for EB_RETENTION_AUDIT"},
	{Name: "migrations", Class: EraseExcluded, Reason: "instance schema history"},
	{Name: "locks", Class: EraseExcluded, Reason: "ephemeral cluster leases"},
	{Name: "oidcProviders", Class: EraseExcluded, Reason: "instance-wide SSO config (P3.4b)"},
	// W72 queue page: one record per queued system mail. Instance-level (no organizationId), holds only the
	// recipient and mail kind (never codes or links) and expires after 7 days (TTL index).
	{Name: "sysmailJobs", Class: EraseExcluded, Reason: "instance-level system-mail records (W72): no organisation; 7-day TTL"},
}

// OrgEraseCollections returns names deleted by organizationId during org erasure
// (excludes special-cased organizations / settings / jobs).
func OrgEraseCollections() []string {
	var out []string
	for _, e := range CollectionRegistry {
		switch e.Class {
		case EraseByOrganization, EraseBoth:
			switch e.Name {
			case "organizations", "settings", "jobs":
				continue
			}
			out = append(out, e.Name)
		}
	}
	return out
}

// AccountEraseTargets returns collection + filter key for account erasure
// (excludes the accounts document itself).
func AccountEraseTargets() []struct {
	Coll string
	Key  AccountEraseKey
} {
	var out []struct {
		Coll string
		Key  AccountEraseKey
	}
	for _, e := range CollectionRegistry {
		switch e.Class {
		case EraseByAccount, EraseBoth:
			if e.Name == "accounts" {
				continue
			}
			key := e.AccountKey
			if key == "" {
				key = AccountKeyAccountID
			}
			out = append(out, struct {
				Coll string
				Key  AccountEraseKey
			}{e.Name, key})
		}
	}
	return out
}

// RegistryByName returns the entry for name, or false.
func RegistryByName(name string) (CollectionEntry, bool) {
	for _, e := range CollectionRegistry {
		if e.Name == name {
			return e, true
		}
	}
	return CollectionEntry{}, false
}
