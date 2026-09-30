package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// stepDeps is a wizardDeps for one step: scripted stdin, captured stdout,
// a temp config dir, and fakes that record what they were asked.
type stepDeps struct {
	d    *wizardDeps
	out  *bytes.Buffer
	runs []string
}

func newStepDeps(t *testing.T, input, goos string, kind secrets.Kind, why string) *stepDeps {
	t.Helper()
	var out bytes.Buffer
	s := &stepDeps{out: &out}
	s.d = &wizardDeps{
		in: strings.NewReader(input), out: &out,
		configDir: t.TempDir(), downloads: t.TempDir(), binary: "/opt/mmg", goos: goos,
		detect:   func(string) (secrets.Kind, string) { return kind, why },
		open:     func(k secrets.Kind) (secrets.Store, error) { return secrets.NewMem(), nil },
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run: func(name string, args ...string) (string, error) {
			s.runs = append(s.runs, name+" "+strings.Join(args, " "))
			return "", nil
		},
		openURL: func(string) error { return nil },
	}
	return s
}

func TestWizardStoreRecordedKindSkips(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "should not be called")
	if err := config.Save(s.d.configDir, &config.File{Version: 1, Secrets: "secret-service"}); err != nil {
		t.Fatal(err)
	}
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindSecretService || !accepted {
		t.Fatalf("kind %q accepted %v err %v", kind, accepted, err)
	}
	if !strings.Contains(s.out.String(), "Secrets go to Secret Service keyring (secret-tool)") || strings.Contains(s.out.String(), "should not be called") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardStoreDarwin(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "Secrets go to the macOS Keychain.")
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindKeychain || !accepted {
		t.Fatalf("kind %q accepted %v err %v", kind, accepted, err)
	}
	if !strings.Contains(s.out.String(), "Secrets go to the macOS Keychain.") || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardStoreLinuxWithoutKeyringAsks(t *testing.T) {
	why := "No keyring found: secret-tool is not installed."
	s := newStepDeps(t, "n\n", "linux", secrets.KindFile, why)
	_, _, err := wizardStore(s.d)
	if !errors.Is(err, errStoreRefused) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(s.out.String(), why) || !strings.Contains(s.out.String(), "Store tokens in a private file, readable by your user account and root? [y/N]") {
		t.Fatalf("out = %q", s.out.String())
	}
	if entries, _ := os.ReadDir(s.d.configDir); len(entries) != 0 {
		t.Fatalf("nothing may be written on refusal: %v", entries)
	}

	s = newStepDeps(t, "y\n", "linux", secrets.KindFile, why)
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !accepted {
		t.Fatalf("kind %q accepted %v err %v", kind, accepted, err)
	}
	if entries, _ := os.ReadDir(s.d.configDir); len(entries) != 0 {
		t.Fatalf("w1 must not write; setup records the kind later: %v", entries)
	}
}

func TestWizardStoreWindowsNeedsNoAcceptance(t *testing.T) {
	s := newStepDeps(t, "", "windows", secrets.KindFile, "This version has no keyring store for Windows.")
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !accepted || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("kind %q accepted %v err %v out %q", kind, accepted, err, s.out.String())
	}
}

func TestWizardStoreEnvForcedNeedsNoAcceptance(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "Secret store forced to file by MINUAR_MULTI_GMAIL_SECRETS.")
	s.d.envKind = "file"
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !accepted || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("kind %q accepted %v err %v out %q", kind, accepted, err, s.out.String())
	}
}

func TestWizardStoreUnknownEnvKind(t *testing.T) {
	s := newStepDeps(t, "", "linux", "", `unknown secret store "vault"`)
	if _, _, err := wizardStore(s.d); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("err = %v", err)
	}
}

func TestWizardClaudeNotOnPath(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	wizardClaude(s.d)
	if len(s.runs) != 0 || !strings.Contains(s.out.String(), "Claude Code is not on PATH") || !strings.Contains(s.out.String(), "claude mcp add --scope user gmail -- /opt/mmg serve") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeAlreadyRegistered(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	wizardClaude(s.d)
	if strings.Join(s.runs, ";") != "claude mcp get gmail" || !strings.Contains(s.out.String(), "Already registered in Claude Code") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeRegisters(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	s.d.run = func(name string, args ...string) (string, error) {
		s.runs = append(s.runs, name+" "+strings.Join(args, " "))
		if args[1] == "get" {
			return "No MCP server found with name: gmail", errors.New("exit status 1")
		}
		return "Added stdio MCP server gmail", nil
	}
	wizardClaude(s.d)
	want := "claude mcp get gmail;claude mcp add --scope user gmail -- /opt/mmg serve"
	if strings.Join(s.runs, ";") != want || !strings.Contains(s.out.String(), "Registered in Claude Code") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeAddFailureIsNotFatal(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	s.d.run = func(name string, args ...string) (string, error) {
		return "boom", errors.New("exit status 1")
	}
	wizardClaude(s.d)
	if !strings.Contains(s.out.String(), "boom") || !strings.Contains(s.out.String(), "claude mcp add --scope user gmail -- /opt/mmg serve") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestManualRegisterCommand(t *testing.T) {
	if got := manualRegisterCommand("/opt/mmg"); got != "claude mcp add --scope user gmail -- /opt/mmg serve" {
		t.Fatalf("got %q", got)
	}
	_ = filepath.Join // keep the import used for later tasks' tests in this file
}
