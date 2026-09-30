package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// promptDeps is a wizardDeps with a scripted stdin and a captured stdout.
func promptDeps(input string) (*wizardDeps, *bytes.Buffer) {
	var out bytes.Buffer
	return &wizardDeps{in: strings.NewReader(input), out: &out}, &out
}

func TestAskReturnsDefaultOnEmptyLine(t *testing.T) {
	d, out := promptDeps("\n")
	got, err := ask(d, "Alias", "work")
	if err != nil || got != "work" {
		t.Fatalf("ask = %q, %v", got, err)
	}
	if out.String() != "Alias [work]: " {
		t.Fatalf("prompt = %q", out.String())
	}
}

func TestAskReturnsTypedAnswerTrimmed(t *testing.T) {
	d, _ := promptDeps("  house \n")
	got, err := ask(d, "Alias", "work")
	if err != nil || got != "house" {
		t.Fatalf("ask = %q, %v", got, err)
	}
}

func TestAskWithoutDefaultShowsNoBrackets(t *testing.T) {
	d, out := promptDeps("x\n")
	if _, err := ask(d, "Path", ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Path: " {
		t.Fatalf("prompt = %q", out.String())
	}
}

func TestAskEOFIsInputEnded(t *testing.T) {
	d, _ := promptDeps("")
	_, err := ask(d, "Alias", "work")
	if !errors.Is(err, errInputEnded) {
		t.Fatalf("err = %v, want errInputEnded", err)
	}
}

// A last line without a newline still counts as an answer.
func TestAskLastLineWithoutNewline(t *testing.T) {
	d, _ := promptDeps("house")
	got, err := ask(d, "Alias", "work")
	if err != nil || got != "house" {
		t.Fatalf("ask = %q, %v", got, err)
	}
}

func TestAskYesNo(t *testing.T) {
	cases := []struct {
		input string
		def   bool
		want  bool
	}{
		{"\n", true, true}, {"\n", false, false},
		{"y\n", false, true}, {"YES\n", false, true},
		{"n\n", true, false}, {"No\n", true, false},
		{"maybe\nyes\n", false, true},
	}
	for _, c := range cases {
		d, out := promptDeps(c.input)
		got, err := askYesNo(d, "Continue?", c.def)
		if err != nil || got != c.want {
			t.Fatalf("input %q def %v: got %v, %v", c.input, c.def, got, err)
		}
		wantPrompt := "Continue? [Y/n]: "
		if !c.def {
			wantPrompt = "Continue? [y/N]: "
		}
		if !strings.HasPrefix(out.String(), wantPrompt) {
			t.Fatalf("input %q: prompt = %q, want prefix %q", c.input, out.String(), wantPrompt)
		}
		if c.input == "maybe\nyes\n" && strings.Count(out.String(), "Continue?") != 2 {
			t.Fatalf("an unknown answer must re-ask: %q", out.String())
		}
	}
	d, _ := promptDeps("")
	if _, err := askYesNo(d, "Continue?", true); !errors.Is(err, errInputEnded) {
		t.Fatalf("EOF: err = %v", err)
	}
}

func TestHeading(t *testing.T) {
	d, out := promptDeps("")
	heading(d, "Secret store")
	if out.String() != "\n== Secret store\n" {
		t.Fatalf("heading = %q", out.String())
	}
}

func TestWizardCommandRefusesWithoutTerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"wizard"}, stdio{out: &out, err: &errOut})
	if code != 2 || !strings.Contains(errOut.String(), "the wizard needs a terminal; run it from your shell") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}
