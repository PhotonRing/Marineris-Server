// Package auth implements administrator authentication: argon2id password
// hashes, TOTP, recovery codes, passkeys (WebAuthn), sessions and login
// throttling.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. They match Authelia's defaults so the same hash
// format can be written to Authelia's users file.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	// Upper bounds accepted when verifying, so a tampered hash cannot make
	// verification consume unbounded memory or CPU.
	maxArgonMemory = 1024 * 1024
	maxArgonTime   = 16
	maxArgonPar    = 64
)

// Password length limits (in characters).
const (
	MinPasswordLen = 12
	MaxPasswordLen = 256
)

// ErrWeakPassword describes why a password was rejected.
type ErrWeakPassword struct{ Reason string }

func (e ErrWeakPassword) Error() string { return "password rejected: " + e.Reason }

// hashSem bounds concurrent argon2 computations (64 MiB each) so a flood of
// login attempts cannot exhaust memory.
var hashSem = make(chan struct{}, 4)

func acquire(ctx context.Context) error {
	select {
	case hashSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release() { <-hashSem }

// HashPassword returns a PHC-formatted argon2id hash of password.
func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := acquire(ctx); err != nil {
		return "", err
	}
	defer release()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

type phc struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

func parsePHC(hash string) (*phc, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, errors.New("auth: unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, errors.New("auth: unsupported argon2 version")
	}
	var p phc
	var threads uint32
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &threads); err != nil || n != 3 {
		return nil, errors.New("auth: malformed argon2 parameters")
	}
	if p.memory < 8 || p.memory > maxArgonMemory || p.time < 1 || p.time > maxArgonTime || threads < 1 || threads > maxArgonPar {
		return nil, errors.New("auth: argon2 parameters out of range")
	}
	p.threads = uint8(threads)
	var err error
	if p.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(p.salt) < 8 {
		return nil, errors.New("auth: malformed salt")
	}
	if p.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(p.key) < 16 || len(p.key) > 128 {
		return nil, errors.New("auth: malformed hash")
	}
	return &p, nil
}

// VerifyPassword reports whether password matches the PHC hash.
func VerifyPassword(ctx context.Context, hash, password string) (bool, error) {
	p, err := parsePHC(hash)
	if err != nil {
		return false, err
	}
	if err := acquire(ctx); err != nil {
		return false, err
	}
	defer release()
	key := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key))) //nolint:gosec // len(p.key) <= 128, checked in parsePHC
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

// dummyHash is verified against when a username does not exist, so that
// the response time does not reveal whether an account exists.
var dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHRzb21lc2FsdA$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI"

// BurnPasswordCheck spends the same work as a real verification.
func BurnPasswordCheck(ctx context.Context, password string) {
	_, _ = VerifyPassword(ctx, dummyHash, password)
}

var commonPasswords = map[string]struct{}{
	"password1234": {}, "123456789012": {}, "qwertyuiop12": {}, "administrator": {},
	"changemechangeme": {}, "letmein123456": {}, "passwordpassword": {}, "marinerisadmin": {},
	"admin1234567": {}, "welcome12345": {}, "iloveyou1234": {}, "000000000000": {},
	"111111111111": {}, "abcdefghijkl": {}, "qwertyqwerty": {}, "trustno1trustno1": {},
}

// CheckPasswordPolicy validates a new password for the given username.
func CheckPasswordPolicy(username, password string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case !utf8.ValidString(password):
		return ErrWeakPassword{"must be valid UTF-8"}
	case n < MinPasswordLen:
		return ErrWeakPassword{fmt.Sprintf("must be at least %d characters", MinPasswordLen)}
	case n > MaxPasswordLen:
		return ErrWeakPassword{fmt.Sprintf("must be at most %d characters", MaxPasswordLen)}
	}
	lower := strings.ToLower(password)
	if username != "" && strings.Contains(lower, strings.ToLower(username)) {
		return ErrWeakPassword{"must not contain the username"}
	}
	if _, ok := commonPasswords[lower]; ok {
		return ErrWeakPassword{"is too common"}
	}
	distinct := map[rune]struct{}{}
	for _, r := range password {
		distinct[r] = struct{}{}
	}
	if len(distinct) < 5 {
		return ErrWeakPassword{"is too repetitive"}
	}
	return nil
}
