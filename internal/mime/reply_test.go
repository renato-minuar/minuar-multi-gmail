package mime

import (
	"strings"
	"testing"
)

func TestReplySubject(t *testing.T) {
	cases := map[string]string{
		"Hello":      "Re: Hello",
		"Re: Hello":  "Re: Hello",
		"RE: Hello":  "RE: Hello",
		"re:Hello":   "re:Hello",
		"":           "Re: ",
		"Fwd: Hello": "Re: Fwd: Hello",
	}
	for in, want := range cases {
		if got := ReplySubject(in); got != want {
			t.Errorf("ReplySubject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReplyRecipientsDefaultsToReplyToThenFrom(t *testing.T) {
	to, cc, err := ReplyRecipients(Original{From: "Ann <ann@x.com>", ReplyTo: "list@x.com"}, "me@example.com", false, nil)
	if err != nil || strings.Join(to, ",") != "<list@x.com>" || len(cc) != 0 {
		t.Fatalf("to=%v cc=%v err=%v", to, cc, err)
	}
	to, _, err = ReplyRecipients(Original{From: "Ann <ann@x.com>"}, "me@example.com", false, nil)
	if err != nil || strings.Join(to, ",") != `"Ann" <ann@x.com>` {
		t.Fatalf("to=%v err=%v", to, err)
	}
}

func TestReplyRecipientsExplicitToWins(t *testing.T) {
	to, cc, err := ReplyRecipients(Original{From: "ann@x.com"}, "me@example.com", false, []string{"bob@x.com"})
	if err != nil || strings.Join(to, ",") != "<bob@x.com>" || len(cc) != 0 {
		t.Fatalf("to=%v cc=%v err=%v", to, cc, err)
	}
}

func TestReplyAllExcludesSelfAndDuplicates(t *testing.T) {
	o := Original{
		From: "ann@x.com",
		To:   []string{"Me <ME@example.com>", "bob@x.com", "ann@x.com"},
		Cc:   []string{"carol@x.com", "Bob <bob@x.com>"},
	}
	to, cc, err := ReplyRecipients(o, "me@example.com", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(to, ",") != "<ann@x.com>" {
		t.Fatalf("to = %v", to)
	}
	if strings.Join(cc, ",") != "<bob@x.com>,<carol@x.com>" {
		t.Fatalf("cc = %v (want bob and carol once each, self and ann excluded)", cc)
	}
}

func TestReplyRecipientsErrorsWhenNobodyLeft(t *testing.T) {
	_, _, err := ReplyRecipients(Original{}, "me@example.com", false, nil)
	if err == nil || !strings.Contains(err.Error(), "no recipients") {
		t.Fatalf("err = %v", err)
	}
}

func TestReplyRecipientsRejectsBadExplicitAddress(t *testing.T) {
	_, _, err := ReplyRecipients(Original{From: "ann@x.com"}, "me@example.com", false, []string{"bad"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReplyRecipientsRejectsHeaderInjectionInExplicitTo(t *testing.T) {
	for name, addr := range map[string]string{
		"CRLF": "a@x.com\r\nBcc: evil@x.com",
		"LF":   "a@x.com\nBcc: evil@x.com",
	} {
		if _, _, err := ReplyRecipients(Original{From: "ann@x.com"}, "me@example.com", false, []string{addr}); err == nil {
			t.Errorf("%s: expected error, addr = %q", name, addr)
		}
	}
}

func TestDedupeCcSkipsSelfAndDuplicates(t *testing.T) {
	to := []string{"<bob@example.com>"}
	cc := []string{"<carol@example.com>"}
	extra := []string{"<control@example.com>", `"Bob" <bob@example.com>`, "<dan@example.com>"}
	got := DedupeCc(cc, to, "control@example.com", extra)
	if strings.Join(got, ",") != "<carol@example.com>,<dan@example.com>" {
		t.Fatalf("got = %v (self control@example.com and duplicate bob must be dropped, dan kept)", got)
	}
}
