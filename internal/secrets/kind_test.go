package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestParseKind(t *testing.T) {
	for _, s := range []string{"keychain", "secret-service", "file"} {
		k, err := ParseKind(s)
		if err != nil || string(k) != s {
			t.Fatalf("ParseKind(%q) = %q, %v", s, k, err)
		}
	}
	if _, err := ParseKind("vault"); err == nil || !strings.Contains(err.Error(), "keychain, secret-service, file") {
		t.Fatalf("ParseKind(vault) = %v, want the list of kinds", err)
	}
	if _, err := ParseKind(""); err == nil {
		t.Fatal("empty kind accepted")
	}
}

func fixedDeps(goos string, env map[string]string, tools map[string]bool, probeErr error) detectDeps {
	return detectDeps{
		goos:   goos,
		getenv: func(k string) string { return env[k] },
		lookPath: func(name string) (string, error) {
			if tools[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		probe: func(string) error { return probeErr },
	}
}

func TestDetectEnvironmentWins(t *testing.T) {
	d := fixedDeps("darwin", map[string]string{EnvKind: "file"}, nil, nil)
	k, why := detect("svc", d)
	if k != KindFile || !strings.Contains(why, EnvKind) {
		t.Fatalf("detect = %q, %q", k, why)
	}
	d = fixedDeps("linux", map[string]string{EnvKind: "vault"}, nil, nil)
	k, why = detect("svc", d)
	if k != "" || !strings.Contains(why, "vault") {
		t.Fatalf("unknown env kind: detect = %q, %q", k, why)
	}
}

func TestDetectPerSystem(t *testing.T) {
	k, why := detect("svc", fixedDeps("darwin", nil, nil, nil))
	if k != KindKeychain || !strings.Contains(why, "Keychain") {
		t.Fatalf("darwin: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("windows", nil, nil, nil))
	if k != KindFile || !strings.Contains(why, "secrets.json") {
		t.Fatalf("windows: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("linux", nil, map[string]bool{"secret-tool": true}, nil))
	if k != KindSecretService || !strings.Contains(why, "Secret Service") {
		t.Fatalf("linux with a keyring: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("linux", nil, map[string]bool{"secret-tool": true}, errors.New("Cannot autolaunch D-Bus")))
	if k != KindFile || !strings.Contains(why, "Cannot autolaunch D-Bus") || !strings.Contains(why, "libsecret-tools") {
		t.Fatalf("linux with a dead keyring: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("linux", nil, nil, nil))
	if k != KindFile || !strings.Contains(why, "secret-tool") || !strings.Contains(why, "libsecret-tools") {
		t.Fatalf("linux without secret-tool: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("freebsd", nil, nil, nil))
	if k != KindFile {
		t.Fatalf("other unix: %q, %q", k, why)
	}
}

func TestDetectNamesLeftoverProbeItem(t *testing.T) {
	probeErr := errors.New("the keyring accepted the write but could not clear the probe item probe-abc: boom")
	k, why := detect("svc", fixedDeps("linux", nil, map[string]bool{"secret-tool": true}, probeErr))
	if k != KindFile || !strings.Contains(why, "could not clear the probe item probe-abc") || !strings.Contains(why, "libsecret-tools") {
		t.Fatalf("leftover probe item: %q, %q", k, why)
	}
}

func TestOpenOn(t *testing.T) {
	dir := "/tmp/cfg"
	if s, err := OpenOn(KindKeychain, "svc", dir, "darwin"); err != nil || s == nil {
		t.Fatalf("keychain on darwin: %v", err)
	}
	if _, err := OpenOn(KindKeychain, "svc", dir, "linux"); err == nil || !strings.Contains(err.Error(), "keychain") || !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("keychain on linux = %v", err)
	}
	if s, err := OpenOn(KindSecretService, "svc", dir, "linux"); err != nil || s == nil {
		t.Fatalf("secret-service on linux: %v", err)
	}
	if _, err := OpenOn(KindSecretService, "svc", dir, "windows"); err == nil || !strings.Contains(err.Error(), "secret-service") {
		t.Fatalf("secret-service on windows = %v", err)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if s, err := OpenOn(KindFile, "svc", dir, goos); err != nil || s == nil {
			t.Fatalf("file on %s: %v", goos, err)
		}
	}
	if _, err := OpenOn("vault", "svc", dir, "linux"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestDescribeKind(t *testing.T) {
	if got := KindKeychain.Describe("/tmp/cfg"); got != "macOS Keychain" {
		t.Fatalf("keychain = %q", got)
	}
	if got := KindSecretService.Describe("/tmp/cfg"); got != "Secret Service keyring (secret-tool)" {
		t.Fatalf("secret-service = %q", got)
	}
	if got := KindFile.Describe("/tmp/cfg"); got != "file /tmp/cfg/secrets.json" {
		t.Fatalf("file = %q", got)
	}
}
