package suppression

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P5")
	const readPerm = "Requires the `read` permission (every role; API keys `suppression:read`). "
	const writePerm = "Requires `outbound.write` (owner, admin, operator; API keys `outbound:write`). "
	auth := []httpapi.ProblemDoc{{Code: errs.CodeUnauthenticated}, {Code: errs.CodeForbidden},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the scope this operation needs."}}
	problems := func(extra ...httpapi.ProblemDoc) map[string]any {
		return httpapi.ProblemDocs(append(append([]httpapi.ProblemDoc{}, auth...), extra...)...)
	}
	listNF := httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The suppression list does not exist."}
	nameTaken := httpapi.ProblemDoc{Code: errs.CodeSuppressionListNameTaken, Detail: "A suppression list with this name already exists."}
	httpapi.Register(server, huma.Operation{
		OperationID: "listSuppressionLists", Method: http.MethodGet, Path: "/v1/suppression-lists",
		Summary: "List suppression lists", Tags: []string{"suppression-lists"},
		Description: "Lists the organisation's suppression lists: your own `manual` lists and the system lists " +
			"(`unsubscribe`, `bounce`, `complaint`) EmailBridge creates when the first unsubscribe, hard bounce or " +
			"complaint arrives. Every send (when the `sending.suppression` switch is on) is checked against all lists. " +
			"Filters: `kind`, `q` (part of the name). Newest first by default; `sort` accepts `createdAt`, `id` and " +
			"`name` (prefix `-` for descending), but the order always follows the list ID (creation order) — only the " +
			"direction changes. Paged by `cursor` or `page` " +
			"(+ `includeTotal`, capped at 10 000); `limit` 1–200 (default 50). " + readPerm + "Read-only.",
		Metadata: problems(httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "q must be at most 200 characters."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleListPage())}},
		Extensions: httpapi.WithScope(phase, "suppression-lists:read"),
	}, m.listLists)
	httpapi.Register(server, huma.Operation{
		OperationID: "createSuppressionList", Method: http.MethodPost, Path: "/v1/suppression-lists",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create a suppression list", Tags: []string{"suppression-lists"},
		Description: "Creates a suppression list (`kind` `manual` by default). Names are unique per organisation " +
			"(`409 suppression_list_name_taken`); there is at most one list of each system kind (`unsubscribe`, " +
			"`bounce`, `complaint` → `409 suppression_list_kind_taken`). " + writePerm + "Returns `Location` and `ETag`.",
		Metadata: problems(nameTaken,
			httpapi.ProblemDoc{Code: errs.CodeSuppressionListKindTaken, Detail: "A system suppression list of this kind already exists."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "kind must be manual, unsubscribe, bounce or complaint."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExample(map[string]any{
			"name": "Do not contact", "kind": "manual", "description": "Customers who asked by phone not to be emailed.",
		})},
		Responses:  map[string]*huma.Response{"201": {Content: jsonExample(exampleList())}},
		Extensions: httpapi.WithScope(phase, "suppression-lists:write"),
	}, m.createList)
	httpapi.Register(server, huma.Operation{
		OperationID: "getSuppressionList", Method: http.MethodGet, Path: "/v1/suppression-lists/{listId}",
		Summary: "Get a suppression list", Tags: []string{"suppression-lists"},
		Description: "Returns one suppression list with its `ETag` (send it as `If-Match` on PATCH / DELETE). " +
			readPerm + "Read-only.",
		Metadata:   problems(listNF),
		Responses:  map[string]*huma.Response{"200": {Content: jsonExample(exampleList())}},
		Extensions: httpapi.WithScope(phase, "suppression-lists:read"),
	}, m.getList)
	httpapi.Register(server, huma.Operation{
		OperationID: "patchSuppressionList", Method: http.MethodPatch, Path: "/v1/suppression-lists/{listId}",
		Summary: "Update a suppression list", Tags: []string{"suppression-lists"},
		Description: "Renames a list or changes its description (JSON Merge Patch; `kind` cannot change). Send the last " +
			"`ETag` as `If-Match` (`412 version_mismatch`). " + writePerm,
		Metadata: problems(listNF, nameTaken, httpapi.ProblemDoc{Code: errs.CodeVersionMismatch},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "description must be at most 512 characters."}),
		RequestBody: &huma.RequestBody{Content: jsonExample(map[string]any{
			"name": "Do not contact (phone requests)", "description": "Updated after the October call-centre review.",
		})},
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(func() map[string]any {
			l := exampleList()
			l["name"] = "Do not contact (phone requests)"
			l["description"] = "Updated after the October call-centre review."
			l["version"] = 2
			l["updatedAt"] = "2026-10-27T12:05:00Z"
			return l
		}())}},
		Extensions: httpapi.WithScope(phase, "suppression-lists:write"),
	}, m.patchList)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteSuppressionList", Method: http.MethodDelete, Path: "/v1/suppression-lists/{listId}",
		Summary: "Delete a suppression list", Tags: []string{"suppression-lists"},
		Description: "Deletes the list and all its entries and answers `204`: its addresses can be sent to again. A " +
			"system list is recreated (empty) at the next unsubscribe, bounce or complaint. `If-Match` is optional. " +
			writePerm + "Not reversible.",
		Metadata:   problems(listNF, httpapi.ProblemDoc{Code: errs.CodeVersionMismatch}),
		Extensions: httpapi.WithScope(phase, "suppression-lists:write"),
	}, m.deleteList)
	httpapi.Register(server, huma.Operation{
		OperationID: "listSuppressionEntries", Method: http.MethodGet, Path: "/v1/suppression-lists/{listId}/entries",
		Summary: "List suppression entries", Tags: []string{"suppression-lists"},
		Description: "Lists the suppressed addresses of a list, ordered by entry ID (list ID + address). `q` matches " +
			"part of the address. Paged by `cursor` or `page` (+ `includeTotal`, capped at 10 000); `limit` 1–200 " +
			"(default 50). Bulk download: `GET …/entries:export` (CSV). " + readPerm + "Read-only.",
		Metadata: problems(listNF, httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "q must be at most 200 characters."}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExample(map[string]any{
			"data":       []any{exampleEntry(), exampleImportedEntry()},
			"nextCursor": "eyJpZCI6InN1cF8wMW01ZG4ybnowazF0Mm5nbTF6dHBxeTU3dzpnaXVsaWEudmVyZGlAZXhhbXBsZS5vcmciLCJmaWVsZCI6Il9pZCIsImRlc2MiOmZhbHNlfQ",
		})}},
		Extensions: httpapi.WithScope(phase, "suppression-lists:read"),
	}, m.listEntries)
	httpapi.Register(server, huma.Operation{
		OperationID: "createSuppressionEntry", Method: http.MethodPost, Path: "/v1/suppression-lists/{listId}/entries",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a suppression entry", Tags: []string{"suppression-lists"},
		Description: "Adds one address (normalised to lower case; `Name <addr>` is accepted) with an optional reason. " +
			"Idempotent per address: `201` when added, `200` with the existing entry when the address was already on " +
			"the list (its reason is not changed). From now on sends to this address fail with `422 " +
			"recipient_suppressed`. Bulk add: `POST …/entries:import` (CSV). " + writePerm,
		Metadata: problems(listNF,
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "address is not a valid e-mail address."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused}),
		RequestBody: &huma.RequestBody{Content: jsonExample(map[string]any{
			"address": "Marco Bianchi <Marco.Bianchi@example.org>", "reason": "Asked by phone on 2026-10-26 not to be emailed.",
		})},
		Responses: map[string]*huma.Response{
			"201": {Description: "Added.", Content: jsonExample(exampleEntry())},
			"200": {Description: "Already on the list: the existing entry.", Content: jsonExample(exampleEntry())},
		},
		Extensions: httpapi.WithScope(phase, "suppression-lists:write"),
	}, m.createEntry)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteSuppressionEntry", Method: http.MethodDelete,
		Path:    "/v1/suppression-lists/{listId}/entries/{entryId}",
		Summary: "Delete a suppression entry", Tags: []string{"suppression-lists"},
		Description: "Removes one address from the list and answers `204`; sends to it are allowed again (unless " +
			"another list holds it). The entry ID is `<listId>:<address>` — URL-encode it in the path. " + writePerm,
		Metadata:   problems(listNF, httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The suppression entry does not exist."}),
		Extensions: httpapi.WithScope(phase, "suppression-lists:write"),
	}, m.deleteEntry)
}

// documentCSV documents the raw CSV import / export routes (W70).
func (m *Module) documentCSV(server *httpapi.Server) {
	listParam := &huma.Param{Name: "listId", In: "path", Required: true, Description: "Suppression list ID (sup_…).",
		Schema: &huma.Schema{Type: huma.TypeString}, Example: exampleListID}
	auth := []httpapi.ProblemDoc{{Code: errs.CodeUnauthenticated}, {Code: errs.CodeForbidden},
		{Code: errs.CodeScopeMissing, Detail: "The API key lacks the scope this operation needs."},
		{Code: errs.CodeNotFound, Detail: "The suppression list does not exist."}}
	server.Document(&huma.Operation{
		OperationID: "importSuppressionEntries", Method: http.MethodPost,
		Path:    "/v1/suppression-lists/{listId}/entries:import",
		Summary: "Import suppression entries from CSV", Tags: []string{"suppression-lists"},
		Description: "Adds many addresses at once from a CSV body (`address[,reason]` per row; a header row " +
			"`address` / `email` is skipped; reasons are cut at 256 characters). At most 10 000 rows and 2 MiB (a larger " +
			"body is `413 payload_too_large`, never a partial import). A leading `'` written by the export in front of " +
			"`= + - @ TAB CR` is removed again. Existing addresses are counted as `skipped` (not changed), unparsable ones " +
			"as `invalid`. Entries get `source: import`. Requires `outbound.write` (owner, admin, operator; API keys " +
			"`outbound:write`).",
		Parameters: []*huma.Param{listParam},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"text/csv": {
			Schema:  &huma.Schema{Type: huma.TypeString, Description: "CSV rows address[,reason], UTF-8."},
			Example: "address,reason\nmarco.bianchi@example.org,Asked by phone not to be emailed\ngiulia.verdi@example.org,\nnot-an-address,\n",
		}}},
		Responses: map[string]*huma.Response{"200": {Description: "Import summary.", Content: map[string]*huma.MediaType{
			"application/json": {
				Schema: &huma.Schema{Type: huma.TypeObject, Required: []string{"imported", "skipped", "invalid"},
					Properties: map[string]*huma.Schema{
						"imported": {Type: huma.TypeInteger, Description: "Addresses added to the list."},
						"skipped":  {Type: huma.TypeInteger, Description: "Addresses already on the list."},
						"invalid":  {Type: huma.TypeInteger, Description: "Rows whose address could not be parsed."},
					}},
				Example: map[string]any{"imported": 1, "skipped": 1, "invalid": 1},
			},
		}}},
		Metadata: httpapi.ProblemDocs(append(auth,
			httpapi.ProblemDoc{Code: errs.CodeMalformedRequest, Detail: "Cannot read body."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "CSV import allows at most 10000 rows."},
			httpapi.ProblemDoc{Code: errs.CodePayloadTooLarge, Detail: "The CSV body exceeds 2 MiB."},
			httpapi.ProblemDoc{Code: errs.CodeIdempotencyKeyReused})...),
		Extensions: httpapi.WithScope(httpapi.Phase("P5"), "suppression-lists:write"),
	})
	server.Document(&huma.Operation{
		OperationID: "exportSuppressionEntries", Method: http.MethodGet,
		Path:    "/v1/suppression-lists/{listId}/entries:export",
		Summary: "Export suppression entries as CSV", Tags: []string{"suppression-lists"},
		Description: "Downloads every entry of the list as CSV (`address,reason,source,at`, with a header row) for " +
			"backups or other systems. Cells starting with `= + - @ TAB CR` are prefixed with `'` so spreadsheet apps " +
			"never run them as formulas (CSV injection). Requires the `read` permission (every role; API keys `suppression:read`). Read-only.",
		Parameters: []*huma.Param{listParam},
		Responses: map[string]*huma.Response{"200": {Description: "CSV file.",
			Headers: map[string]*huma.Param{"Content-Disposition": {Description: "Always suppression.csv.",
				Schema: &huma.Schema{Type: huma.TypeString}, Example: `attachment; filename="suppression.csv"`}},
			Content: map[string]*huma.MediaType{"text/csv": {
				Schema:  &huma.Schema{Type: huma.TypeString, Description: "CSV: address,reason,source,at (RFC 3339 UTC)."},
				Example: "address,reason,source,at\nmarco.bianchi@example.org,Asked by phone not to be emailed,manual,2026-10-27T12:01:00Z\ngiulia.verdi@example.org,,import,2026-10-27T12:03:10Z\n",
			}}}},
		Metadata:   httpapi.ProblemDocs(auth...),
		Extensions: httpapi.WithScope(httpapi.Phase("P5"), "suppression-lists:read"),
	})
}

// Documentation examples (W70).
const exampleListID = "sup_01m5dn2nz0k1t2ngm1ztpqy57w"

func jsonExample(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleList() map[string]any {
	return map[string]any{
		"id": exampleListID, "organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "name": "Do not contact",
		"kind": "manual", "description": "Customers who asked by phone not to be emailed.",
		"createdAt": "2026-10-27T12:00:00Z", "updatedAt": "2026-10-27T12:00:00Z", "version": 1,
	}
}

func exampleListPage() map[string]any {
	bounces := map[string]any{
		"id": "sup_01m5dq6dy04398xhrxtppqnypx", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "name": "Bounces",
		"kind": "bounce", "createdAt": "2026-10-22T07:14:31Z", "updatedAt": "2026-10-22T07:14:31Z", "version": 1,
	}
	return map[string]any{"data": []any{exampleList(), bounces}, "nextCursor": nil}
}

func exampleEntry() map[string]any {
	return map[string]any{
		"id": exampleListID + ":marco.bianchi@example.org", "listId": exampleListID,
		"organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "address": "marco.bianchi@example.org",
		"reason": "Asked by phone on 2026-10-26 not to be emailed.", "source": "manual", "at": "2026-10-27T12:01:00Z",
	}
}

func exampleImportedEntry() map[string]any {
	return map[string]any{
		"id": exampleListID + ":giulia.verdi@example.org", "listId": exampleListID,
		"organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "address": "giulia.verdi@example.org",
		"source": "import", "at": "2026-10-27T12:03:10Z",
	}
}

type listListsIn struct {
	Limit        int    `query:"limit" doc:"Page size 1–200 (default 50)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6InN1cF8wMW01ZHE2ZHkwNDM5OHhocnh0cHBxbnlweCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total (capped at 10 000, then totalCapped is true)." example:"true"`
	Sort         string `query:"sort" doc:"createdAt, id or name; prefix - for descending. Default -createdAt (newest first)." example:"-createdAt"`
	Q            string `query:"q" doc:"Case-insensitive part of the list name (≤ 200 characters)." example:"contact"`
	Kind         string `query:"kind" doc:"Only lists of this kind: manual, unsubscribe, bounce or complaint." example:"manual"`
}

type listListsOut struct {
	Body ListPage
}

type createListIn struct {
	Body CreateListInput
}

type createListOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new list." example:"/v1/suppression-lists/sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	ETag     string `header:"ETag" doc:"Version of the new list." example:"\"v1\""`
	Body     List
}

type listIDIn struct {
	ListID string `path:"listId" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
}

type getListOut struct {
	ETag string `header:"ETag" doc:"Version of the list; send it as If-Match on the next write." example:"\"v2\""`
	Body List
}

type patchListIn struct {
	ListID  string `path:"listId" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	IfMatch string `header:"If-Match"`
	Body    PatchListInput
}

type deleteListIn struct {
	ListID  string `path:"listId" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	IfMatch string `header:"If-Match"`
}

type listEntriesIn struct {
	ListID       string `path:"listId" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	Limit        int    `query:"limit" doc:"Page size 1–200 (default 50)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6InN1cF8wMW01ZG4ybnowazF0Mm5nbTF6dHBxeTU3dzpnaXVsaWEudmVyZGlAZXhhbXBsZS5vcmciLCJmaWVsZCI6Il9pZCIsImRlc2MiOmZhbHNlfQ"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"Also return total (capped at 10 000, then totalCapped is true)." example:"true"`
	Q            string `query:"q" doc:"Case-insensitive part of the address (≤ 200 characters)." example:"example.org"`
}

type listEntriesOut struct {
	Body EntryPage
}

type createEntryIn struct {
	ListID string `path:"listId" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	Body   CreateEntryInput
}

type createEntryOut struct {
	Status int
	Body   Entry
}

type deleteEntryIn struct {
	ListID  string `path:"listId" doc:"Suppression list ID (sup_…)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w"`
	EntryID string `path:"entryId" doc:"Entry ID: <listId>:<address> (URL-encoded)." example:"sup_01m5dn2nz0k1t2ngm1ztpqy57w:marco.bianchi@example.org"`
}

func (m *Module) listLists(ctx context.Context, in *listListsIn) (*listListsOut, error) {
	res, err := m.svc.ListLists(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Q: in.Q, Kind: in.Kind,
	})
	if err != nil {
		return nil, err
	}
	return &listListsOut{Body: *res}, nil
}

func (m *Module) createList(ctx context.Context, in *createListIn) (*createListOut, error) {
	l, err := m.svc.CreateList(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &createListOut{
		Status: http.StatusCreated, Location: "/v1/suppression-lists/" + l.ID,
		ETag: formatETag(l.Version), Body: *l,
	}, nil
}

func (m *Module) getList(ctx context.Context, in *listIDIn) (*getListOut, error) {
	l, err := m.svc.GetList(ctx, in.ListID)
	if err != nil {
		return nil, err
	}
	return &getListOut{ETag: formatETag(l.Version), Body: *l}, nil
}

func (m *Module) patchList(ctx context.Context, in *patchListIn) (*getListOut, error) {
	l, err := m.svc.PatchList(ctx, in.ListID, in.IfMatch, in.Body)
	if err != nil {
		return nil, err
	}
	return &getListOut{ETag: formatETag(l.Version), Body: *l}, nil
}

func (m *Module) deleteList(ctx context.Context, in *deleteListIn) (*struct{}, error) {
	return nil, m.svc.DeleteList(ctx, in.ListID, in.IfMatch)
}

func (m *Module) listEntries(ctx context.Context, in *listEntriesIn) (*listEntriesOut, error) {
	res, err := m.svc.ListEntries(ctx, EntryFilter{
		ListID: in.ListID, Limit: in.Limit, Cursor: in.Cursor, Page: in.Page,
		IncludeTotal: in.IncludeTotal, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listEntriesOut{Body: *res}, nil
}

func (m *Module) createEntry(ctx context.Context, in *createEntryIn) (*createEntryOut, error) {
	e, created, err := m.svc.AddEntry(ctx, in.ListID, in.Body)
	if err != nil {
		return nil, err
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	return &createEntryOut{Status: status, Body: *e}, nil
}

func (m *Module) deleteEntry(ctx context.Context, in *deleteEntryIn) (*struct{}, error) {
	return nil, m.svc.DeleteEntry(ctx, in.ListID, in.EntryID)
}
