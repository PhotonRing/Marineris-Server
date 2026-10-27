package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// RecoveryCodeCount is the number of one-time recovery codes issued.
const RecoveryCodeCount = 10

// recovery code alphabet: no ambiguous characters (0/O, 1/l/I).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns fresh codes (to show once) and their hashes (to
// store). Each code carries 100 bits of entropy, so a plain SHA-256 is an
// adequate one-way function.
func NewRecoveryCodes() (codes, hashes []string) {
	for i := 0; i < RecoveryCodeCount; i++ {
		c := randomString(20, recoveryAlphabet)
		c = c[0:5] + "-" + c[5:10] + "-" + c[10:15] + "-" + c[15:20]
		codes = append(codes, c)
		hashes = append(hashes, HashRecoveryCode(c))
	}
	return codes, hashes
}

// HashRecoveryCode normalises and hashes a recovery code.
func HashRecoveryCode(code string) string {
	norm := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte("marineris-recovery:" + norm))
	return hex.EncodeToString(sum[:])
}

func randomString(n int, alphabet string) string {
	out := make([]byte, n)
	max := byte(256 - 256%len(alphabet)) //nolint:gosec // alphabet is short and constant; rejection sampling avoids modulo bias
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			panic("auth: crypto/rand failed: " + err.Error())
		}
		if buf[0] >= max {
			continue
		}
		out[i] = alphabet[int(buf[0])%len(alphabet)]
		i++
	}
	return string(out)
}
