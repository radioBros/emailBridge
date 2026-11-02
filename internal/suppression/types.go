// Package suppression owns organisation suppression lists and entries (P5.2).
package suppression

import "time"

const (
	ListsCollection   = "suppressionLists"
	EntriesCollection = "suppressionEntries"

	KindManual      = "manual"
	KindUnsubscribe = "unsubscribe"
	KindBounce      = "bounce"
	KindComplaint   = "complaint"

	SourceManual      = "manual"
	SourceUnsubscribe = "unsubscribe"
	SourceBounce      = "bounce"
	SourceComplaint   = "complaint"
	SourceImport      = "import"
)

// List is a suppressionLists document.
type List struct {
	ID             string    `bson:"_id" json:"id" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	OrganizationID string    `bson:"organizationId" json:"organizationId" doc:"Organisation (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Name           string    `bson:"name" json:"name" doc:"Unique name in the organisation (1–128 characters)." example:"Do not contact"`
	Kind           string    `bson:"kind" json:"kind" doc:"manual (yours), or a system list: unsubscribe, bounce, complaint." example:"manual"`
	Description    string    `bson:"description,omitempty" json:"description,omitempty" doc:"Free text, ≤ 512 characters." example:"Customers who asked by phone not to be emailed."`
	CreatedAt      time.Time `bson:"createdAt" json:"createdAt" doc:"When the list was created." example:"2026-10-27T12:00:00Z"`
	UpdatedAt      time.Time `bson:"updatedAt" json:"updatedAt" doc:"Last change." example:"2026-10-27T12:00:00Z"`
	Version        int64     `bson:"version" json:"version" doc:"Optimistic-concurrency version (the ETag is \"v<version>\")." example:"1"`
	CreatedBy      string    `bson:"createdBy,omitempty" json:"-"`
}

// Entry is a suppressionEntries document.
type Entry struct {
	ID             string    `bson:"_id" json:"id" doc:"Entry ID: <listId>:<address> (plus :<mailingListId> for a list-scoped unsubscribe)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w:marco.bianchi@example.org"`
	ListID         string    `bson:"listId" json:"listId" doc:"List the entry belongs to (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	OrganizationID string    `bson:"organizationId" json:"organizationId" doc:"Organisation (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Address        string    `bson:"address" json:"address" doc:"Suppressed address, lower case." example:"marco.bianchi@example.org"`
	Reason         string    `bson:"reason,omitempty" json:"reason,omitempty" doc:"Why it is suppressed (≤ 256 characters): your note, or the bounce / complaint diagnostic." example:"Asked by phone on 2026-10-26 not to be emailed."`
	Source         string    `bson:"source" json:"source" doc:"How it got here: manual, import, unsubscribe, bounce or complaint." example:"manual"`
	At             time.Time `bson:"at" json:"at" doc:"When the address was added." example:"2026-10-27T12:01:00Z"`
	// MailingListID scopes an unsubscribe to one RFC List-Id (W53). Empty = global.
	MailingListID string `bson:"mailingListId,omitempty" json:"mailingListId,omitempty" doc:"Unsubscribe from one mailing list only (its RFC 2919 List-Id); absent = every send." example:"newsletter.example.com"`
}

// CreateListInput is POST /v1/suppression-lists.
type CreateListInput struct {
	Name        string `json:"name" doc:"Unique name in the organisation, 1–128 characters (required)." example:"Do not contact"`
	Kind        string `json:"kind,omitempty" doc:"manual (default), unsubscribe, bounce or complaint (one list per system kind)." example:"manual"`
	Description string `json:"description,omitempty" doc:"Free text, ≤ 512 characters." example:"Customers who asked by phone not to be emailed."`
}

// PatchListInput is PATCH /v1/suppression-lists/{id}.
type PatchListInput struct {
	Name        *string `json:"name,omitempty" doc:"New unique name." example:"Do not contact (phone requests)"`
	Description *string `json:"description,omitempty" doc:"New description (empty string clears it)." example:"Updated after the October call-centre review."`
}

// CreateEntryInput is POST …/entries.
type CreateEntryInput struct {
	Address string `json:"address" doc:"Address to suppress; a display-name form is accepted (required)." example:"Marco Bianchi <Marco.Bianchi@example.org>"`
	Reason  string `json:"reason,omitempty" doc:"Why (≤ 256 characters)." example:"Asked by phone on 2026-10-26 not to be emailed."`
}

// ListPage is a cursor/page response of lists.
type ListPage struct {
	Data        []List  `json:"data" doc:"The page of lists."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6InN1cF8wMW01ZHE2ZHkwNDM5OHhocnh0cHBxbnlweCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Total       *int64  `json:"total,omitempty" doc:"Number of matching lists, with includeTotal=true (at most 10 000)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"true when total stopped at 10 000." example:"false"`
}

// EntryPage is a cursor/page response of entries.
type EntryPage struct {
	Data        []Entry `json:"data" doc:"The page of entries."`
	NextCursor  *string `json:"nextCursor" doc:"Cursor for the next page; null on the last page." example:"eyJpZCI6InN1cF8wMW01ZG4ybnowazF0Mm5nbTF6dHBxeTU3dzpnaXVsaWEudmVyZGlAZXhhbXBsZS5vcmciLCJmaWVsZCI6Il9pZCIsImRlc2MiOmZhbHNlfQ"`
	Total       *int64  `json:"total,omitempty" doc:"Number of matching entries, with includeTotal=true (at most 10 000)." example:"2"`
	TotalCapped *bool   `json:"totalCapped,omitempty" doc:"true when total stopped at 10 000." example:"false"`
}

// ListFilter filters list queries.
type ListFilter struct {
	Limit, Page  int
	Cursor, Sort string
	IncludeTotal bool
	Q, Kind      string
}

// EntryFilter filters entry queries.
type EntryFilter struct {
	ListID       string
	Limit, Page  int
	Cursor, Sort string
	IncludeTotal bool
	Q            string
}

// ImportResult is the CSV import summary.
type ImportResult struct {
	Imported int `json:"imported" doc:"Addresses added to the list." example:"1"`
	Skipped  int `json:"skipped" doc:"Addresses already on the list." example:"1"`
	Invalid  int `json:"invalid" doc:"Rows whose address could not be parsed." example:"1"`
}

// Hit is one suppressed recipient for submit errors.
type Hit struct {
	Address       string `json:"address" doc:"Suppressed recipient." example:"marco.bianchi@example.org"`
	ListID        string `json:"listId" doc:"List that holds it (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	Reason        string `json:"reason,omitempty" doc:"Reason stored on the entry." example:"Asked by phone on 2026-10-26 not to be emailed."`
	Source        string `json:"source" doc:"manual, import, unsubscribe, bounce or complaint." example:"manual"`
	Kind          string `json:"kind,omitempty" doc:"Kind of the list." example:"manual"`
	MailingListID string `json:"mailingListId,omitempty" doc:"List-Id of a list-scoped unsubscribe." example:"newsletter.example.com"`
}
