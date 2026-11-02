package settings

import (
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
)

// Collections (S1).
const (
	SettingsCollection     = "settings"
	UserSettingsCollection = "userSettings"
)

// DocIDGlobal is the _id of the instance-wide settings document.
const DocIDGlobal = "global"

// OrgDocID returns the settings document id for an organisation.
func OrgDocID(orgID string) string { return "org:" + orgID }

// SwitchValue is one entry under settings.switches.
type SwitchValue struct {
	Value  any  `bson:"value" json:"value"`
	Locked bool `bson:"locked,omitempty" json:"locked,omitempty"`
}

// secretStored is how encrypted secret runtime values are persisted in values.
type secretStored struct {
	crypto.EncryptedString `bson:",inline"`
	Last4                  string `bson:"last4"`
	Secret                 bool   `bson:"_secret"`
}

// Doc is one settings document (S1).
type Doc struct {
	ID        string                 `bson:"_id"`
	Values    map[string]any         `bson:"values,omitempty"`
	Switches  map[string]SwitchValue `bson:"switches,omitempty"`
	Env       map[string]any         `bson:"env,omitempty"`
	Version   int64                  `bson:"version"`
	UpdatedAt time.Time              `bson:"updatedAt,omitempty"`
}

// UserDoc is S1 userSettings.
type UserDoc struct {
	ID        string         `bson:"_id"` // userId
	Settings  map[string]any `bson:"settings"`
	UpdatedAt time.Time      `bson:"updatedAt"`
	Version   int64          `bson:"version"`
}

// SettingValue is one entry in GET /v1/settings (OpenAPI SettingValue).
type SettingValue struct {
	Value  any    `json:"value" doc:"Effective value (secrets masked as ••••<last 4>)." example:"\"30s\""`
	Source string `json:"source" enum:"default,env,global,organization" doc:"Where the effective value comes from (organisation → global → environment → default)." example:"global"` // default | env | global | organization
	Scope  string `json:"scope" enum:"global,organization" doc:"Scope at which the value can be changed with PATCH /v1/settings." example:"organization"`                             // global | organization
	Locked bool   `json:"locked,omitempty" doc:"True when the instance owner locked the value at global scope (organisations cannot override it)." example:"false"`
}

// SettingsBody is the GET/PATCH /v1/settings response body.
type SettingsBody struct {
	Values   map[string]SettingValue `json:"values" doc:"Every runtime setting by name, with its effective value and source."`
	Switches map[string]SettingValue `json:"switches" doc:"Every feature switch by name, with its effective value and source."`
	Version  int64                   `json:"version" doc:"Settings version (also the ETag)." example:"7"`
}

// FeatureSwitchBody is one GET /v1/feature-switches item.
type FeatureSwitchBody struct {
	Name        string   `json:"name" doc:"Switch name." example:"sandbox.enabled"`
	Description string   `json:"description" doc:"What the switch turns on or off." example:"Capture outgoing mail instead of sending"`
	Scopes      []string `json:"scopes" doc:"Levels at which the switch can be set (global, organization, account, endpoint)." example:"[\"global\",\"organization\",\"account\"]"`
	Default     any      `json:"default" doc:"Built-in default value." example:"false"`
	Phase       string   `json:"phase" doc:"Build phase that introduced the switch." example:"P1"`
	Lockable    bool     `json:"lockable,omitempty" doc:"Whether the instance owner can lock it globally." example:"true"`
	Locked      bool     `json:"locked,omitempty" doc:"Whether it is currently locked at global scope." example:"false"`
	Effective   any      `json:"effective" doc:"Effective value for the caller's organisation." example:"true"`
	Source      string   `json:"source,omitempty" doc:"Where the effective value comes from (default, global, organization)." example:"organization"`
}

// UserSettingsBody is GET/PATCH /v1/me/settings.
type UserSettingsBody struct {
	Settings  map[string]any `json:"settings" doc:"The user's settings object: tables.<tableName>.{visibility,order,widths}, ui.{theme,density,locale}, notifications.{mutedCategories,email} and other UI preferences (≤ 64 KiB)." example:"{\"tables\":{\"accounts\":{\"visibility\":[\"email\",\"state\",\"lastSyncAt\"],\"order\":[\"email\",\"state\",\"lastSyncAt\"],\"widths\":{\"email\":280}}},\"ui\":{\"theme\":\"dark\",\"density\":\"compact\",\"locale\":\"it\"}}"`
	UpdatedAt time.Time      `json:"updatedAt" doc:"Last change (zero time before the first save)." example:"2026-10-26T17:45:03Z"`
	Version   int64          `json:"version,omitempty" doc:"Version (also the ETag); absent before the first save." example:"4"`
}
