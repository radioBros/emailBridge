package suppression

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ImportCSV upserts addresses from a CSV stream (address[,reason]).
func (s *Service) ImportCSV(ctx context.Context, listID string, r io.Reader) (*ImportResult, error) {
	if err := tenancy.Require(ctx, tenancy.PermSend); err != nil {
		return nil, err
	}
	l, err := s.Repo.getList(ctx, listID)
	if err != nil {
		return nil, err
	}
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	res := &ImportResult{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errs.New(errs.CodeValidationFailed, "CSV is malformed.")
		}
		if len(rec) == 0 {
			continue
		}
		addr := unescapeCSVCell(strings.TrimSpace(rec[0]))
		if addr == "" || strings.EqualFold(addr, "address") || strings.EqualFold(addr, "email") {
			continue
		}
		if res.Imported+res.Skipped+res.Invalid >= maxImport {
			return nil, errs.New(errs.CodeValidationFailed, "CSV import allows at most 10000 rows.").
				WithSafe("limit", maxImport)
		}
		norm, nerr := normalizeAddress(addr)
		if nerr != nil {
			res.Invalid++
			continue
		}
		reason := ""
		if len(rec) > 1 {
			reason = unescapeCSVCell(strings.TrimSpace(rec[1]))
			if utf8.RuneCountInString(reason) > maxReason {
				reason = string([]rune(reason)[:maxReason])
			}
		}
		e := &Entry{
			ID: entryID(l.ID, norm), ListID: l.ID, OrganizationID: l.OrganizationID,
			Address: norm, Reason: reason, Source: SourceImport, At: s.now(),
		}
		created, err := s.Repo.upsertEntry(ctx, e)
		if err != nil {
			return nil, err
		}
		if created {
			res.Imported++
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

// ExportCSV writes address,reason rows.
func (s *Service) ExportCSV(ctx context.Context, listID string, w io.Writer) error {
	if err := tenancy.RequireRead(ctx, "suppression"); err != nil {
		return err
	}
	l, err := s.Repo.getList(ctx, listID)
	if err != nil {
		return err
	}
	rows, err := s.Repo.allEntries(ctx, l.OrganizationID, l.ID)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"address", "reason", "source", "at"})
	for _, e := range rows {
		_ = cw.Write([]string{csvSafe(e.Address), csvSafe(e.Reason), csvSafe(e.Source), e.At.UTC().Format("2006-01-02T15:04:05Z")})
	}
	cw.Flush()
	return cw.Error()
}

// formulaTriggers are the leading characters spreadsheet apps evaluate as a formula.
const formulaTriggers = "=+-@\t\r"

// csvSafe neutralises spreadsheet formula injection (OWASP "CSV injection"): a cell that starts with
// = + - @ TAB or CR is prefixed with a single quote so spreadsheet apps show it as text.
func csvSafe(v string) string {
	if v != "" && strings.IndexByte(formulaTriggers, v[0]) >= 0 {
		return "'" + v
	}
	return v
}

// unescapeCSVCell reverses csvSafe on import, so an exported file re-imports to the same values.
func unescapeCSVCell(v string) string {
	if len(v) > 1 && v[0] == '\'' && strings.IndexByte(formulaTriggers, v[1]) >= 0 {
		return v[1:]
	}
	return v
}
