package gmail

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"blocks and inline", "<p>One <b>two</b></p><p>Three</p>", "One two\n\nThree"},
		{"br", "a<br>b<br/>c", "a\nb\nc"},
		{"skips script style head", "<head><title>T</title><style>x{}</style></head><body>Body<script>bad()</script></body>", "Body"},
		{"entities", "Fish &amp; chips &lt;3 &euro;5", "Fish & chips <3 €5"},
		{"collapses blank lines", "<p>a</p><p></p><p></p><p>b</p>", "a\n\nb"},
		{"list items", "<ul><li>x</li><li>y</li></ul>", "x\n\ny"},
		{"whitespace runs", "<div>  many    spaces\n\n here </div>", "many spaces here"},
		{"plain text passthrough", "just text", "just text"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := htmlToText(c.in); got != c.want {
			t.Errorf("%s: htmlToText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	s, cut := truncateRunes("héllo", 2)
	if s != "hé" || !cut {
		t.Fatalf("got %q %v", s, cut)
	}
	s, cut = truncateRunes("héllo", 5)
	if s != "héllo" || cut {
		t.Fatalf("got %q %v", s, cut)
	}
	s, cut = truncateRunes("héllo", 0)
	if s != "héllo" || cut {
		t.Fatalf("max 0 means no limit; got %q %v", s, cut)
	}
}
