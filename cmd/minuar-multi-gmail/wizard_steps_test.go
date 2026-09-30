package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
		run: func(_ context.Context, name string, args ...string) (string, error) {
			s.runs = append(s.runs, name+" "+strings.Join(args, " "))
			return "", nil
		},
		openURL: func(string) error { return nil },
		now:     time.Now, sleep: time.Sleep, waitFile: 10 * time.Minute,
	}
	return s
}

func TestWizardStoreRecordedKindSkips(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "should not be called")
	if err := config.Save(s.d.configDir, &config.File{Version: 1, Secrets: "secret-service"}); err != nil {
		t.Fatal(err)
	}
	kind, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindSecretService {
		t.Fatalf("kind %q err %v", kind, err)
	}
	if !strings.Contains(s.out.String(), "Secrets go to Secret Service keyring (secret-tool)") || strings.Contains(s.out.String(), "should not be called") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardStoreDarwin(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "Secrets go to the macOS Keychain.")
	kind, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindKeychain {
		t.Fatalf("kind %q err %v", kind, err)
	}
	if !strings.Contains(s.out.String(), "Secrets go to the macOS Keychain.") || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardStoreLinuxWithoutKeyringAsks(t *testing.T) {
	why := "No keyring found: secret-tool is not installed."
	s := newStepDeps(t, "n\n", "linux", secrets.KindFile, why)
	_, err := wizardStore(s.d)
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
	kind, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile {
		t.Fatalf("kind %q err %v", kind, err)
	}
	if entries, _ := os.ReadDir(s.d.configDir); len(entries) != 0 {
		t.Fatalf("w1 must not write; setup records the kind later: %v", entries)
	}
}

func TestWizardStoreWindowsNeedsNoAcceptance(t *testing.T) {
	s := newStepDeps(t, "", "windows", secrets.KindFile, "This version has no keyring store for Windows.")
	kind, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("kind %q err %v out %q", kind, err, s.out.String())
	}
}

func TestWizardStoreEnvForcedNeedsNoAcceptance(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "Secret store forced to file by MINUAR_MULTI_GMAIL_SECRETS.")
	s.d.envKind = "file"
	kind, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("kind %q err %v out %q", kind, err, s.out.String())
	}
}

func TestWizardStoreUnknownEnvKind(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "")
	s.d.envKind = "vault"
	if _, err := wizardStore(s.d); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("err = %v", err)
	}
}

func TestWizardStoreEnvBeatsRecordedKind(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.detect = func(string) (secrets.Kind, string) { t.Fatal("the probe must not run"); return "", "" }
	s.d.envKind = "file"
	if err := config.Save(s.d.configDir, &config.File{Version: 1, Secrets: "keychain"}); err != nil {
		t.Fatal(err)
	}
	kind, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !strings.Contains(s.out.String(), "Secret store forced to file by MINUAR_MULTI_GMAIL_SECRETS.") {
		t.Fatalf("kind %q err %v out %q", kind, err, s.out.String())
	}
}

func TestWizardStoreUnknownRecordedKind(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	if err := config.Save(s.d.configDir, &config.File{Version: 1, Secrets: "vault"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wizardStore(s.d); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("err = %v", err)
	}
}

func TestWizardClaudeNotOnPath(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	wizardClaude(context.Background(), s.d)
	if len(s.runs) != 0 || !strings.Contains(s.out.String(), "Claude Code is not on PATH") || !strings.Contains(s.out.String(), "claude mcp add --scope user gmail -- /opt/mmg serve") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeAlreadyRegistered(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	wizardClaude(context.Background(), s.d)
	if strings.Join(s.runs, ";") != "claude mcp get gmail" || !strings.Contains(s.out.String(), "Already registered in Claude Code") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeRegisters(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	s.d.run = func(_ context.Context, name string, args ...string) (string, error) {
		s.runs = append(s.runs, name+" "+strings.Join(args, " "))
		if args[1] == "get" {
			return "No MCP server found with name: gmail", errors.New("exit status 1")
		}
		return "Added stdio MCP server gmail", nil
	}
	wizardClaude(context.Background(), s.d)
	want := "claude mcp get gmail;claude mcp add --scope user gmail -- /opt/mmg serve"
	if strings.Join(s.runs, ";") != want || !strings.Contains(s.out.String(), "Registered in Claude Code") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeAddFailureIsNotFatal(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	s.d.run = func(_ context.Context, name string, args ...string) (string, error) {
		return "boom", errors.New("exit status 1")
	}
	wizardClaude(context.Background(), s.d)
	if !strings.Contains(s.out.String(), "boom") || !strings.Contains(s.out.String(), "(exit status 1)") || !strings.Contains(s.out.String(), "claude mcp add --scope user gmail -- /opt/mmg serve") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardClaudeCallsCarryATimeout(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	deadlines := 0
	s.d.run = func(ctx context.Context, name string, args ...string) (string, error) {
		if _, ok := ctx.Deadline(); ok {
			deadlines++
		}
		return "", context.DeadlineExceeded
	}
	wizardClaude(context.Background(), s.d)
	if deadlines != 2 || !strings.Contains(s.out.String(), "Registration failed") || !strings.Contains(s.out.String(), "context deadline exceeded") {
		t.Fatalf("deadlines %d out %q", deadlines, s.out.String())
	}
}

func TestManualRegisterCommand(t *testing.T) {
	if got := manualRegisterCommand("/opt/mmg"); got != "claude mcp add --scope user gmail -- /opt/mmg serve" {
		t.Fatalf("got %q", got)
	}
}

func writeClientFile(t *testing.T, dir, name string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(sampleClientSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

// A clock the watcher advances itself: sleep moves it forward and fires
// the scheduled file write.
func fakeClock(start time.Time) (now func() time.Time, sleep func(time.Duration), after func(time.Duration, func())) {
	t := start
	var jobs []struct {
		at time.Time
		fn func()
	}
	now = func() time.Time { return t }
	sleep = func(d time.Duration) {
		runtime.Gosched()
		time.Sleep(time.Millisecond) // let the stdin goroutine deliver its line
		t = t.Add(d)
		for i := 0; i < len(jobs); i++ {
			if !jobs[i].at.After(t) {
				jobs[i].fn()
				jobs = append(jobs[:i], jobs[i+1:]...)
				i--
			}
		}
	}
	after = func(d time.Duration, fn func()) {
		jobs = append(jobs, struct {
			at time.Time
			fn func()
		}{t.Add(d), fn})
	}
	return
}

func TestWatchDownloadsFindsANewFile(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, 10*time.Minute
	// An old download from an earlier attempt must be ignored.
	writeClientFile(t, s.d.downloads, "client_secret_old.json", start.Add(-time.Hour))
	after(3*time.Second, func() { writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	got, _, err := watchDownloads(s.d, start)
	if err != nil || filepath.Base(got) != "client_secret_new.json" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWatchDownloadsTimesOut(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, _ := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, 5*time.Second
	_, _, err := watchDownloads(s.d, start)
	if err == nil || !strings.Contains(err.Error(), "no client file appeared in "+s.d.downloads+" within 5s") {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchDownloadsAcceptsAPastedPath(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, _ := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	elsewhere := writeClientFile(t, t.TempDir(), "client_secret_x.json", start.Add(-time.Hour))
	s.d.in = strings.NewReader(elsewhere + "\n")
	got, _, err := watchDownloads(s.d, start)
	if err != nil || got != elsewhere {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWatchDownloadsEmptyLineKeepsWaiting(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	s.d.in = strings.NewReader("\n")
	after(30*time.Second, func() { writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	got, _, err := watchDownloads(s.d, start)
	if err != nil || filepath.Base(got) != "client_secret_new.json" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !strings.Contains(s.out.String(), "still waiting for the file in "+s.d.downloads) {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardClientSkipsWhenStored(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	st := seededStore(t)
	opened := 0
	s.d.openURL = func(string) error { opened++; return nil }
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, st); err != nil {
		t.Fatal(err)
	}
	if opened != 0 || !strings.Contains(s.out.String(), "OAuth client stored") {
		t.Fatalf("opened %d out %q", opened, s.out.String())
	}
}

func TestWizardClientGuidesTheConsoleAndStoresTheDownload(t *testing.T) {
	// Enter for pages 1-3, then the file appears while page 4 is open, then
	// "y" to delete the download.
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	w := pipeInput(t, s.d, "\n\n\n")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	var opened []string
	s.d.openURL = func(u string) error { opened = append(opened, u); return nil }
	var path string
	after(2*time.Second, func() {
		path = writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second))
		w.WriteString("y\n")
	})
	st := secrets.NewMem()
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, st); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 4 || opened[0] != consolePages[0].URL || opened[3] != consolePages[3].URL {
		t.Fatalf("opened = %v", opened)
	}
	for _, p := range consolePages {
		if !strings.Contains(s.out.String(), p.Instruction) || !strings.Contains(s.out.String(), p.URL) {
			t.Fatalf("out lacks page %q", p.URL)
		}
	}
	if id, _ := st.Get(secrets.ClientIDKey); id != "123-abc.apps.googleusercontent.com" {
		t.Fatalf("client id = %q", id)
	}
	cfg, _ := config.Load(s.d.configDir)
	if cfg.Secrets != "keychain" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the download must be deleted after y: %v", err)
	}
}

func TestWizardClientKeepsTheDownloadOnNo(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	w := pipeInput(t, s.d, "\n\n\n")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	var path string
	after(2*time.Second, func() {
		path = writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second))
		w.WriteString("n\n")
	})
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the download must stay after n: %v", err)
	}
}

func TestWizardClientEOFWhileWaitingForEnter(t *testing.T) {
	s := newStepDeps(t, "\n", "darwin", secrets.KindKeychain, "")
	err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem())
	if !errors.Is(err, errInputEnded) {
		t.Fatalf("err = %v, want errInputEnded", err)
	}
}

func TestWizardClientRejectsABadFile(t *testing.T) {
	s := newStepDeps(t, "\n\n\n", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	after(time.Second, func() {
		p := filepath.Join(s.d.downloads, "client_secret_bad.json")
		os.WriteFile(p, []byte("{not json"), 0o600)
		os.Chtimes(p, now().Add(time.Second), now().Add(time.Second))
	})
	err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem())
	if err == nil || !strings.Contains(err.Error(), "client_secret_bad.json") {
		t.Fatalf("err = %v", err)
	}
}

// pipeInput feeds stdin through a pipe so a test can type answers at the
// moment a real user would. It writes first and returns the writer.
func pipeInput(t *testing.T, d *wizardDeps, first string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close(); r.Close() })
	d.in = r
	w.WriteString(first)
	return w
}

func TestWatchDownloadsIgnoresALineThatIsNotAFile(t *testing.T) {
	s := newStepDeps(t, "not-a-path\n", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	after(30*time.Second, func() { writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	got, _, err := watchDownloads(s.d, start)
	if err != nil || filepath.Base(got) != "client_secret_new.json" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !strings.Contains(s.out.String(), "not a file: not-a-path") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestNewestClientFileSkipsEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	empty := filepath.Join(dir, "client_secret_empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(empty, start.Add(time.Minute), start.Add(time.Minute))
	if got := newestClientFile(dir, start); got != "" {
		t.Fatalf("an empty file must be ignored, got %q", got)
	}
	full := writeClientFile(t, dir, "client_secret_full.json", start.Add(time.Second))
	if got := newestClientFile(dir, start); got != full {
		t.Fatalf("got %q, want %q", got, full)
	}
}

func TestNormalizePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	spaced := filepath.Join(dir, "my files")
	os.MkdirAll(spaced, 0o755)
	plain := writeClientFile(t, dir, "client_secret_q.json", time.Now())
	withSpace := writeClientFile(t, spaced, "client_secret_s.json", time.Now())
	underHome := writeClientFile(t, home, "client_secret_h.json", time.Now())
	cases := []struct{ in, want string }{
		{"  '" + plain + "'  ", plain},
		{`"` + plain + `"`, plain},
		{strings.ReplaceAll(withSpace, " ", `\ `), withSpace},
		{"~/client_secret_h.json", underHome},
	}
	for _, c := range cases {
		got := normalizePath(c.in)
		if got != c.want || !isRegularFile(got) {
			t.Fatalf("normalizePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWizardClientOffersALeftoverFile(t *testing.T) {
	s := newStepDeps(t, "y\nn\n", "darwin", secrets.KindKeychain, "")
	left := writeClientFile(t, s.d.downloads, "client_secret_left.json", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	opened := 0
	s.d.openURL = func(string) error { opened++; return nil }
	st := secrets.NewMem()
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, st); err != nil {
		t.Fatal(err)
	}
	if id, _ := st.Get(secrets.ClientIDKey); id == "" || opened != 0 {
		t.Fatalf("client id %q opened %d", id, opened)
	}
	out := s.out.String()
	if !strings.Contains(out, "Use "+left+"? [Y/n]") || !strings.Contains(out, "Delete "+left+"? [y/N]") {
		t.Fatalf("out = %q", out)
	}
	if _, err := os.Stat(left); err != nil {
		t.Fatalf("the default for a reused file is to keep it: %v", err)
	}
}

func TestWizardClientDeclinedLeftoverOpensThePages(t *testing.T) {
	s := newStepDeps(t, "n\n\n\n\n", "darwin", secrets.KindKeychain, "")
	writeClientFile(t, s.d.downloads, "client_secret_left.json", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	now, sleep, _ := fakeClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Second
	opened := 0
	s.d.openURL = func(string) error { opened++; return nil }
	err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem())
	if err == nil || !strings.Contains(err.Error(), "no client file appeared") || opened != 4 {
		t.Fatalf("opened %d err %v", opened, err)
	}
}

func TestWizardClientPathPastedAtPageTwoStoresIt(t *testing.T) {
	elsewhere := writeClientFile(t, t.TempDir(), "client_secret_x.json", time.Now())
	s := newStepDeps(t, "\n"+"'"+elsewhere+"'\n"+"y\n", "darwin", secrets.KindKeychain, "")
	var opened []string
	s.d.openURL = func(u string) error { opened = append(opened, u); return nil }
	st := secrets.NewMem()
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, st); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 {
		t.Fatalf("pages opened = %v, want 2", opened)
	}
	if id, _ := st.Get(secrets.ClientIDKey); id == "" {
		t.Fatal("client not stored")
	}
	if !strings.Contains(s.out.String(), "Delete "+elsewhere+"? [y/N]") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardClientOtherTextAtAPageAsksAgain(t *testing.T) {
	s := newStepDeps(t, "done\n\n\n\n", "darwin", secrets.KindKeychain, "")
	now, sleep, _ := fakeClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Second
	wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem())
	if !strings.Contains(s.out.String(), "press Enter when the page is done, or paste the path of the downloaded file") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardClientSkipRecordsTheKind(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, seededStore(t)); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(s.d.configDir)
	if cfg.Secrets != "keychain" || !strings.Contains(s.out.String(), "Recorded the secret store.") {
		t.Fatalf("config %+v out %q", cfg, s.out.String())
	}
}

func TestWizardClientNoDisplayHint(t *testing.T) {
	const hint = "This machine has no display: download the file on your own computer, copy it here (scp), and paste its path."
	for _, c := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"linux", nil, true},
		{"darwin", nil, false},
		{"linux", map[string]string{"DISPLAY": ":0"}, false},
	} {
		s := newStepDeps(t, "\n\n\n", c.goos, secrets.KindFile, "")
		s.d.getenv = func(k string) string { return c.env[k] }
		now, sleep, _ := fakeClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
		s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Second
		wizardClient(context.Background(), s.d, secrets.KindFile, secrets.NewMem())
		if got := strings.Contains(s.out.String(), hint); got != c.want {
			t.Fatalf("goos %s env %v: hint shown %v, want %v", c.goos, c.env, got, c.want)
		}
	}
}
