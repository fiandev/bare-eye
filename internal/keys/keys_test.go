package keys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAndInspect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "id")
	if err := Generate(path, "", "test", false); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %o", st.Mode().Perm())
	}
	ds, _ := os.Stat(filepath.Dir(path))
	if ds.Mode().Perm() != 0o700 {
		t.Errorf("key dir mode = %o", ds.Mode().Perm())
	}
	pub, err := PublicKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Errorf("public key = %q", pub)
	}
	fp, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Errorf("fingerprint = %q", fp)
	}
	if _, err := LoadSigner(path, ""); err != nil {
		t.Errorf("LoadSigner: %v", err)
	}
}

func TestGenerateRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id")
	if err := Generate(path, "", "", false); err != nil {
		t.Fatal(err)
	}
	err := Generate(path, "", "", false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if err := Generate(path, "", "", true); err != nil {
		t.Fatalf("force regenerate: %v", err)
	}
}

func TestPassphraseProtectedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id")
	if err := Generate(path, "correct horse", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSigner(path, ""); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("err = %v, want ErrPassphraseRequired", err)
	}
	if _, err := LoadSigner(path, "wrong"); err == nil {
		t.Fatal("wrong passphrase should fail")
	}
	if _, err := LoadSigner(path, "correct horse"); err != nil {
		t.Fatalf("correct passphrase: %v", err)
	}
}
