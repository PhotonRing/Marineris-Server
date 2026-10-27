// Package secrets provides at-rest encryption for credentials stored by
// Marineris, plus helpers to create random tokens and secret files safely.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	keySize       = 32
	sealedVersion = "v1:"
)

// ErrDecrypt is returned when a sealed value cannot be opened: wrong key,
// tampered ciphertext or a value bound to a different context.
var ErrDecrypt = errors.New("secrets: cannot decrypt value")

// Box encrypts and decrypts values with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// NewBox creates a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext. The context string is authenticated but not
// stored: a value sealed for one context (for example "dns-profile:42")
// cannot be opened under another, which prevents swapping ciphertexts
// between database rows.
func (b *Box) Seal(plaintext []byte, context string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, plaintext, []byte(context))
	return sealedVersion + base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal with the same context.
func (b *Box) Open(sealed, context string) ([]byte, error) {
	rest, ok := strings.CutPrefix(sealed, sealedVersion)
	if !ok {
		return nil, ErrDecrypt
	}
	raw, err := base64.RawStdEncoding.DecodeString(rest)
	if err != nil {
		return nil, ErrDecrypt
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+b.aead.Overhead() {
		return nil, ErrDecrypt
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], []byte(context))
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// LoadOrCreateKey reads the master key at path, creating it when missing.
// The key file must not be readable by group or others.
func LoadOrCreateKey(path string) ([]byte, error) {
	data, err := ReadSecretFile(path)
	if err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(data)))
		if derr != nil || len(key) != keySize {
			return nil, fmt.Errorf("secrets: master key at %s is malformed", path)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := WriteSecretFile(path, []byte(hex.EncodeToString(key)+"\n"), true); err != nil {
		return nil, err
	}
	return key, nil
}

// ReadSecretFile reads a secret file and refuses files whose permissions
// allow access by group or others.
func ReadSecretFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("secrets: %s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secrets: %s has permissions %o; must be 0600 or stricter", path, st.Mode().Perm())
	}
	return os.ReadFile(path) //nolint:gosec // path comes from the process configuration
}

// ReadGroupSecretFile reads a secret file that may be readable by its group
// (mode 0640), for secrets shared with a service in the same group. Files
// readable by others are refused.
func ReadGroupSecretFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("secrets: %s is not a regular file", path)
	}
	if st.Mode().Perm()&0o027 != 0 {
		return nil, fmt.Errorf("secrets: %s has permissions %o; must be 0640 or stricter", path, st.Mode().Perm())
	}
	return os.ReadFile(path) //nolint:gosec // path comes from the process configuration
}

// WriteSecretFile atomically writes data to path with mode 0600, creating
// parent directories with mode 0700. With exclusive set it fails if the file
// already exists.
func WriteSecretFile(path string, data []byte, exclusive bool) error {
	return WriteFileAtomic(path, data, 0o600, exclusive)
}

// WriteFileAtomic writes data to a temporary file next to path and renames
// it into place, so readers never observe a partially written file.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode, exclusive bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if exclusive {
		if _, err := os.Lstat(path); err == nil {
			return fs.ErrExist
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Token returns n random bytes encoded as unpadded URL-safe base64.
func Token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secrets: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HexToken returns n random bytes encoded as lowercase hex.
func HexToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secrets: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Equal compares two strings in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
