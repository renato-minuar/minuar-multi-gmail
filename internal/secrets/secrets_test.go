package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestValidSecret(t *testing.T) {
	good := []string{"GOCSPX-abc_DEF.123", "1//0gABC-xyz_123", "a+b/c=", "x~y"}
	bad := []string{"", "has space", "quote'x", `dq"x`, "semi;colon", "new\nline", "tab\tx", "unicodé"}
	for _, s := range good {
		if !ValidSecret(s) {
			t.Errorf("ValidSecret(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidSecret(s) {
			t.Errorf("ValidSecret(%q) = true, want false", s)
		}
	}
}

func TestMemStore(t *testing.T) {
	m := NewMem()
	if _, err := m.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing err = %v, want ErrNotFound", err)
	}
	if err := m.Set("x", "bad value"); err == nil {
		t.Fatal("invalid secret accepted")
	}
	if err := m.Set("x", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("x", "v2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get("x"); got != "v2" {
		t.Fatalf("Get = %q, want v2", got)
	}
	if err := m.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing err = %v", err)
	}
}

func TestKeyNames(t *testing.T) {
	if RefreshTokenKey("work") != "refresh-token.work" {
		t.Fatal(RefreshTokenKey("work"))
	}
	if !strings.HasPrefix(ClientIDKey, "oauth-") || !strings.HasPrefix(ClientSecretKey, "oauth-") {
		t.Fatal("key names changed")
	}
}

func TestServiceNameEnvOverride(t *testing.T) {
	t.Setenv(EnvService, "")
	if ServiceName() != DefaultService {
		t.Fatal(ServiceName())
	}
	t.Setenv(EnvService, "com.example.test")
	if ServiceName() != "com.example.test" {
		t.Fatal(ServiceName())
	}
}
