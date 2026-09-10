package mime

import "testing"

func TestForwardSubject(t *testing.T) {
	cases := map[string]string{
		"Hello":          "Fwd: Hello",
		"Fwd: Hello":     "Fwd: Hello",
		"FW: Hello":      "FW: Hello",
		"  fwd: Hello":   "  fwd: Hello",
		"Re: Fwd: Hello": "Fwd: Re: Fwd: Hello",
		"":               "Fwd: ",
	}
	for in, want := range cases {
		if got := ForwardSubject(in); got != want {
			t.Errorf("ForwardSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmlFilename(t *testing.T) {
	long := ""
	for i := 0; i < 120; i++ {
		long += "é"
	}
	cases := map[string]string{
		"Invoice 42":     "Invoice 42.eml",
		"a/b\\c":         "a_b_c.eml",
		"tab\there\nnew": "tab_here_new.eml",
		"   ":            "forwarded-message.eml",
		"":               "forwarded-message.eml",
		"  spaced  ":     "spaced.eml",
		"Rechnung März":  "Rechnung März.eml",
		"\x7fdel":        "_del.eml",
		// Windows rejects these in a filename; Gmail hands the .eml to the
		// recipient's file system as it is named here.
		`a:b?c*d<e>f|g"h`: "a_b_c_d_e_f_g_h.eml",
		"Re: report":      "Re_ report.eml",
		long:              long[:100*2] + ".eml", // 100 runes of "é" (2 bytes each)
	}
	for in, want := range cases {
		if got := EmlFilename(in); got != want {
			t.Errorf("EmlFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
