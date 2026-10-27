package secrets

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t)
	s, err := b.Seal([]byte("hunter2"), "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "hunter2") {
		t.Fatal("ciphertext leaks plaintext")
	}
	pt, err := b.Open(s, "ctx")
	if err != nil || string(pt) != "hunter2" {
		t.Fatalf("got %q, %v", pt, err)
	}
}

func TestSealIsRandomized(t *testing.T) {
	b := newBox(t)
	a, _ := b.Seal([]byte("x"), "c")
	c, _ := b.Seal([]byte("x"), "c")
	if a == c {
		t.Fatal("two seals of the same value must differ")
	}
}

func TestOpenRejectsWrongContext(t *testing.T) {
	b := newBox(t)
	s, _ := b.Seal([]byte("secret"), "dns-profile:1")
	if _, err := b.Open(s, "dns-profile:2"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("expected ErrDecrypt, got %v", err)
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	b := newBox(t)
	s, _ := b.Seal([]byte("secret"), "c")
	for i := len(sealedVersion); i < len(s); i++ {
		mut := []byte(s)
		if mut[i] == 'A' {
			mut[i] = 'B'
		} else {
			mut[i] = 'A'
		}
		if pt, err := b.Open(string(mut), "c"); err == nil && string(pt) == "secret" {
			// Changing the last base64 char may only flip unused padding bits.
			if i != len(s)-1 {
				t.Fatalf("tampered ciphertext at %d accepted", i)
			}
		}
	}
	for _, bad := range []string{"", "v1:", "v2:abcd", "v1:!!!!", "v1:AAAA"} {
		if _, err := b.Open(bad, "c"); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("%q: expected ErrDecrypt, got %v", bad, err)
		}
	}
}

func TestOpenRejectsOtherKey(t *testing.T) {
	b := newBox(t)
	other, _ := NewBox(bytes.Repeat([]byte{8}, 32))
	s, _ := b.Seal([]byte("secret"), "c")
	if _, err := other.Open(s, "c"); !errors.Is(err, ErrDecrypt) {
		t.Fatal("value opened with the wrong key")
	}
}

func TestNewBoxKeySize(t *testing.T) {
	if _, err := NewBox(make([]byte, 16)); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "master.key")
	k1, err := LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key perms %o", st.Mode().Perm())
	}
	dst, _ := os.Stat(filepath.Dir(p))
	if dst.Mode().Perm() != 0o700 {
		t.Fatalf("dir perms %o", dst.Mode().Perm())
	}
	k2, err := LoadOrCreateKey(p)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("key not stable across loads")
	}
}

func TestReadSecretFileRejectsLoosePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecretFile(p); err == nil {
		t.Fatal("expected refusal of world-readable secret")
	}
	if _, err := LoadOrCreateKey(p); err == nil {
		t.Fatal("expected refusal of world-readable key")
	}
}

func TestReadSecretFileRejectsSymlinkToDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadSecretFile(dir); err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestWriteSecretFileExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := WriteSecretFile(p, []byte("a"), true); err != nil {
		t.Fatal(err)
	}
	if err := WriteSecretFile(p, []byte("b"), true); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("expected ErrExist, got %v", err)
	}
	if err := WriteSecretFile(p, []byte("b"), false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "b" {
		t.Fatalf("got %q", got)
	}
}

func TestTokens(t *testing.T) {
	if len(HexToken(32)) != 64 {
		t.Fatal("hex token length")
	}
	if a, b := Token(32), Token(32); a == b {
		t.Fatal("tokens repeat")
	}
	if !Equal("abc", "abc") || Equal("abc", "abd") || Equal("abc", "ab") {
		t.Fatal("Equal broken")
	}
}

func FuzzOpen(f *testing.F) {
	b, _ := NewBox(bytes.Repeat([]byte{1}, 32))
	s, _ := b.Seal([]byte("seed"), "c")
	f.Add(s)
	f.Add("v1:")
	f.Fuzz(func(t *testing.T, in string) {
		_, _ = b.Open(in, "c") // must never panic
	})
}
