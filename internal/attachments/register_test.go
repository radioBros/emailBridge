package attachments

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestRegisterMetas_upsertByPart(t *testing.T) {
	s := testSvc(t)
	ctx := store.WithOrganization(context.Background(), "org_01habcdefghijklmnopqrstuvw")
	org, acc, msg := "org_01habcdefghijklmnopqrstuvw", "acc_01habcdefghijklmnopqrstuvw", "msg_01habcdefghijklmnopqrstuvw"
	res, err := s.RegisterMetas(ctx, RegisterInput{
		OrganizationID: org, AccountID: acc, MessageID: msg,
		Metas: []Meta{
			{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", Size: 3, Disposition: "attachment"},
			{PartID: "1.2", Filename: "", ContentType: "image/png", Size: 4, ContentID: "cid1", Disposition: "inline"},
		},
	})
	if err != nil || len(res) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if res[0].ID == "" || res[0].PartID != "2" || res[0].Scan != ScanSkipped {
		t.Fatalf("%+v", res[0])
	}
	if res[1].Disposition != "inline" || res[1].ContentID != "cid1" {
		t.Fatalf("%+v", res[1])
	}
	again, err := s.RegisterMetas(ctx, RegisterInput{
		OrganizationID: org, AccountID: acc, MessageID: msg,
		Metas: []Meta{{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", Size: 3, Disposition: "attachment"}},
	})
	if err != nil || len(again) != 1 || again[0].ID != res[0].ID {
		t.Fatalf("stable id: %+v vs %+v err=%v", again, res[0], err)
	}
}

func TestRegisterMetas_partIDMatchesWalkExtract(t *testing.T) {
	parts, err := listParts(sampleMIME())
	if err != nil || len(parts) == 0 {
		t.Fatal(err)
	}
	s := testSvc(t)
	ctx := store.WithOrganization(context.Background(), "org_01habcdefghijklmnopqrstuvw")
	metas := make([]Meta, len(parts))
	for i, p := range parts {
		metas[i] = Meta{PartID: p.PartID, Filename: p.Filename, ContentType: p.ContentType, Size: int64(len(p.Body)), ContentID: p.ContentID, Disposition: p.Disposition}
	}
	res, err := s.RegisterMetas(ctx, RegisterInput{
		OrganizationID: "org_01habcdefghijklmnopqrstuvw", AccountID: "acc_01habcdefghijklmnopqrstuvw",
		MessageID: "msg_01habcdefghijklmnopqrstuvw", Metas: metas,
	})
	if err != nil || len(res) != len(parts) {
		t.Fatalf("%+v %v", res, err)
	}
	for i := range parts {
		if res[i].PartID != parts[i].PartID {
			t.Fatalf("partId mismatch %q vs %q", res[i].PartID, parts[i].PartID)
		}
	}
}
