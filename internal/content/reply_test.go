package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplyText_corpus(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"basic.txt", "Thanks for the update — I will review today."},
		{"italian.txt", "Perfetto, procediamo così."},
		{"outlook.txt", "Agreed."},
		{"signature.txt", "See you tomorrow."},
	}
	for _, tc := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", "reply", tc.file))
		if err != nil {
			t.Fatal(err)
		}
		got := ReplyText(string(raw))
		if got != tc.want {
			t.Errorf("%s: got %q want %q", tc.file, got, tc.want)
		}
	}
}

func TestReplyText_quotedLines(t *testing.T) {
	in := "New bit\n\n> old\n> more\nAlso new"
	got := ReplyText(in)
	if !strings.Contains(got, "New bit") || !strings.Contains(got, "Also new") || strings.Contains(got, ">") {
		t.Fatalf("%q", got)
	}
}

func TestReplyTextFromHTML(t *testing.T) {
	html := `<p>Thanks</p><blockquote>On Mon Alice wrote:<br>old</blockquote>`
	// attribution may appear after html→text; ensure we get something sensible
	got := ReplyTextFromHTML(`<p>Thanks for confirming.</p><p>On Mon, 1 Jan 2024 Alice wrote:</p><p>&gt; earlier</p>`)
	if got != "Thanks for confirming." {
		t.Fatalf("%q", got)
	}
	_ = html
}

func TestReplyText_empty(t *testing.T) {
	if ReplyText("") != "" || ReplyTextFromHTML("") != "" {
		t.Fatal("empty")
	}
}

func TestReplyText_germanFrench(t *testing.T) {
	de := "Ok.\n\nAm 01.01.2024 schrieb Alice Example:"
	if ReplyText(de) != "Ok." {
		t.Fatalf("de: %q", ReplyText(de))
	}
	fr := "D'accord.\n\nLe 1 janv. 2024 Alice a écrit :"
	if ReplyText(fr) != "D'accord." {
		t.Fatalf("fr: %q", ReplyText(fr))
	}
}
