package content

import (
	"testing"
)

func TestThreadID_referencesPreferFirst(t *testing.T) {
	root := ThreadID("<root@x>", "", nil, "")
	reply := ThreadID("<reply@x>", "<mid@x>", []string{"<root@x>", "<mid@x>"}, "Re: hi")
	deep := ThreadID("<deep@x>", "<mid@x>", []string{"<root@x>", "<mid@x>"}, "Re: hi")
	if reply != root || deep != root {
		t.Fatalf("want all under root=%s; reply=%s deep=%s", root, reply, deep)
	}
}

func TestThreadID_inReplyToWhenNoReferences(t *testing.T) {
	root := ThreadID("<root@x>", "", nil, "")
	reply := ThreadID("<reply@x>", "<root@x>", nil, "Re: hi")
	if reply != root {
		t.Fatalf("in-reply-to: got %s want %s", reply, root)
	}
}

func TestThreadID_bracketsDoNotMatter(t *testing.T) {
	a := ThreadID("<own@x>", "", nil, "")
	b := ThreadID("own@x", "", nil, "")
	if a == "" || a != b {
		t.Fatalf("bracket normalize: %q vs %q", a, b)
	}
}

func TestThreadID_subjectFallback(t *testing.T) {
	a := ThreadID("", "", nil, "Re: Hello World")
	b := ThreadID("", "", nil, "FWD: hello   world")
	c := ThreadID("", "", nil, "AW: Hello World")
	if a == "" || a != b || a != c {
		t.Fatalf("subject fallback: %q %q %q", a, b, c)
	}
	withID := ThreadID("<id@x>", "", nil, "Re: Hello World")
	if withID == a {
		t.Fatal("message-id must win over subject")
	}
}

func TestNormalizeSubject(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Re: Hello", "hello"},
		{"Re[2]: Fwd: Hi", "hi"},
		{"AW: WG: Thema", "thema"},
		{"R: Ciao", "ciao"},
		{"Inoltro: Nota", "nota"},
		{"SV: Hej", "hej"},
		{"  Re:   Spaces   Here  ", "spaces here"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeSubject(tc.in); got != tc.want {
			t.Errorf("NormalizeSubject(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestThreadID_deterministicEmpty(t *testing.T) {
	a := ThreadID("", "", nil, "")
	b := ThreadID("", "", nil, "   ")
	if a != b || len(a) != 24 {
		t.Fatalf("empty subject fallback: %q %q", a, b)
	}
}
