package suppression

import "testing"

func TestFilterHitsForList(t *testing.T) {
	hits := []Hit{
		{Address: "a@x.test", Kind: KindBounce},
		{Address: "a@x.test", Kind: KindUnsubscribe, MailingListID: ""},
		{Address: "a@x.test", Kind: KindUnsubscribe, MailingListID: "news.example.com"},
		{Address: "a@x.test", Kind: KindUnsubscribe, MailingListID: "other.example.com"},
		{Address: "a@x.test", Kind: KindManual},
	}
	got := filterHitsForList(hits, "news.example.com")
	if len(got) != 4 { // bounce, global unsub, news unsub, manual — not other
		t.Fatalf("%+v", got)
	}
	got = filterHitsForList(hits, "")
	if len(got) != 3 { // bounce, global, manual — not list-scoped
		t.Fatalf("%+v", got)
	}
	got = filterHitsForList(hits, "other.example.com")
	if len(got) != 4 {
		t.Fatalf("%+v", got)
	}
}

func TestEntryIDScoped(t *testing.T) {
	if entryIDScoped("sup_1", "a@b.test", "") != "sup_1:a@b.test" {
		t.Fatal("global")
	}
	if entryIDScoped("sup_1", "a@b.test", "news") != "sup_1:a@b.test:news" {
		t.Fatal("scoped")
	}
}
