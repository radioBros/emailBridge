//go:build integration

package exports

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

type putCounter struct{ n int }

func (p *putCounter) IncPut() { p.n++ }

func TestIntegrationMultipartPutCountAndResume(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, js := newSvc(t, db, objs)
	svc.BatchSize = 50
	counter := &putCounter{}
	svc.PutHook = counter

	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	const n = 2000
	seedMessages(t, db, org, acc, mbx, n)
	ctx := asOwner(org)

	a, err := svc.Create(ctx, acc, CreateInput{Include: Include{MetadataOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": a.Export.ID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantParts := (n + svc.BatchSize - 1) / svc.BatchSize
	if counter.n != wantParts {
		t.Fatalf("puts=%d want %d (O(parts))", counter.n, wantParts)
	}
	full, err := svc.Get(ctx, acc, a.Export.ID)
	if err != nil || full.State != StateSucceeded || full.MessageCount != int64(n) {
		t.Fatalf("%+v %v", full, err)
	}
	if lines := countLines(downloadNDJSON(t, svc, ctx, acc, a.Export.ID)); lines != n {
		t.Fatalf("lines=%d want %d", lines, n)
	}

	// Resume: seed first part + checkpoint, then finish remaining messages.
	acc2 := store.NewID(store.PrefixAccount)
	mbx2 := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc2, mbx2, 5)
	svc.BatchSize = 2
	counter.n = 0
	a2, err := svc.Create(ctx, acc2, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := svc.Repo.getByID(ctx, org, a2.Export.ID)
	e2.State = StateRunning
	fk, err := svc.ensureFileKey(ctx, e2)
	if err != nil {
		t.Fatal(err)
	}
	var nd bytes.Buffer
	for i := 1; i <= 2; i++ {
		_ = json.NewEncoder(&nd).Encode(Line{
			ID: store.NewID(store.PrefixMessage), MailboxID: mbx2, UID: uint32(i),
		})
	}
	var gzb bytes.Buffer
	zw := gzip.NewWriter(&gzb)
	_, _ = zw.Write(nd.Bytes())
	_ = zw.Close()
	enc, err := sealAESGCM(fk, gzb.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := objstore.ExportPrefix(org, e2.ID)
	pk := prefix + "part-000001.ndjson.gz"
	if err := objs.Put(ctx, pk, bytes.NewReader(enc), int64(len(enc)), objstore.ObjectMeta{
		Organization: org, Account: acc2,
	}); err != nil {
		t.Fatal(err)
	}
	e2.Parts = []string{pk}
	e2.Progress = &Progress{
		Done: 2, Total: 5, Checkpoint: &Checkpoint{MailboxID: mbx2, UID: 2},
		NextPart: 2, PartCount: 1,
	}
	if err := svc.Repo.replace(ctx, e2); err != nil {
		t.Fatal(err)
	}

	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": e2.ID, "accountId": acc2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, acc2, e2.ID)
	if err != nil || got.State != StateSucceeded || got.MessageCount != 5 {
		t.Fatalf("%+v %v", got, err)
	}
	if counter.n < 1 || counter.n > 3 {
		t.Fatalf("resume puts=%d want O(remaining parts)", counter.n)
	}
	uids := map[uint32]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(downloadNDJSON(t, svc, ctx, acc2, e2.ID)), []byte("\n")) {
		var l Line
		if err := json.Unmarshal(line, &l); err != nil {
			t.Fatal(err)
		}
		uids[l.UID]++
	}
	if len(uids) != 5 {
		t.Fatalf("unique uids=%d", len(uids))
	}
	for u, c := range uids {
		if c != 1 {
			t.Fatalf("uid %d duplicated %d", u, c)
		}
	}
}

func TestIntegrationEraseHookRemovesObjects(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	accounts.ClearEraseHooksForTest()
	svc, js := newSvc(t, db, objs)
	accounts.RegisterEraseHook("exports", func(ctx context.Context, orgID, accountID string) error {
		_, err := svc.EraseAccount(ctx, orgID, accountID)
		return err
	})
	t.Cleanup(accounts.ClearEraseHooksForTest)

	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	mbx := store.NewID(store.PrefixMailbox)
	seedMessages(t, db, org, acc, mbx, 3)
	ctx := asOwner(org)
	a, err := svc.Create(ctx, acc, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(ctx, JobKind, org, map[string]string{"exportId": a.Export.ID, "accountId": acc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := objstore.ExportPrefix(org, a.Export.ID)
	var before int
	_ = objs.List(ctx, prefix, func(objstore.ObjectInfo) error { before++; return nil })
	if before == 0 {
		t.Fatal("expected export objects before erase")
	}

	if err := accounts.RunEraseHooksForTest(ctx, org, acc); err != nil {
		t.Fatal(err)
	}
	var after int
	_ = objs.List(ctx, prefix, func(objstore.ObjectInfo) error { after++; return nil })
	if after != 0 {
		t.Fatalf("objects left after erase: %d", after)
	}
	if _, err := svc.Get(ctx, acc, a.Export.ID); errs.CodeOf(err) != errs.CodeExportNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationLegacySingleBlobDownload(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	svc, _ := newSvc(t, db, objs)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := asOwner(org)

	e := &Export{
		ID: store.NewID(store.PrefixExport), OrganizationID: org, AccountID: acc,
		State: StateSucceeded, CreatedAt: svc.now(),
	}
	fk := bytes.Repeat([]byte{9}, 32)
	wrapped, err := svc.Cipher.Encrypt(ctx, org, fk, crypto.AAD{
		Collection: Collection, Field: "fileKey", DocID: e.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Encryption = wrapped
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"uid":1}` + "\n"))
	_ = zw.Close()
	enc, err := sealAESGCM(fk, gz.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := objstore.ExportPrefix(org, e.ID)
	e.ObjectRef = prefix + "data.ndjson.gz.enc"
	if err := objs.Put(ctx, e.ObjectRef, bytes.NewReader(enc), int64(len(enc)), objstore.ObjectMeta{
		Organization: org, Account: acc,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Repo.insert(ctx, e); err != nil {
		t.Fatal(err)
	}
	raw := downloadNDJSON(t, svc, ctx, acc, e.ID)
	if !bytes.Contains(raw, []byte(`"uid":1`)) {
		t.Fatalf("%q", raw)
	}
}

func downloadNDJSON(t *testing.T, svc *Service, ctx context.Context, acc, id string) []byte {
	t.Helper()
	rc, _, err := svc.OpenDownload(ctx, acc, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	b, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func countLines(b []byte) int {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return 0
	}
	return bytes.Count(b, []byte("\n")) + 1
}
