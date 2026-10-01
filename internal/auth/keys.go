// Package auth generates and checks user API keys and the admin token, and
// provides the middleware that enforces them.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// Prefix starts every user key: lgai_ + base64url(32 random bytes) = 48 chars.
const Prefix = "lgai_"

const (
	keyLen        = len(Prefix) + 43
	displayPrefix = 12 // characters kept as key_prefix for humans
)

var keyEncoding = base64.RawURLEncoding.Strict()

// Generate returns a new random key, its SHA-256 and its 12-char display prefix.
func Generate() (plaintext string, hash [32]byte, prefix string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", hash, "", err
	}
	plaintext = Prefix + keyEncoding.EncodeToString(b[:])
	return plaintext, Hash(plaintext), plaintext[:displayPrefix], nil
}

// Hash is the stored form of a key. A fast unsalted hash is right for a
// uniformly random 256-bit key (ARCHITECTURE §7, security §3.1).
func Hash(plaintext string) [32]byte { return sha256.Sum256([]byte(plaintext)) }

// WellFormed checks prefix, length and canonical base64url so garbage never
// reaches the database.
func WellFormed(s string) bool {
	if len(s) != keyLen || !strings.HasPrefix(s, Prefix) {
		return false
	}
	b, err := keyEncoding.DecodeString(s[len(Prefix):])
	return err == nil && len(b) == 32
}

// BearerToken extracts the token from exactly one "Authorization: Bearer x"
// header (scheme case-insensitive). Keys are accepted nowhere else: not in the
// query string, cookies or X-API-Key (security #14).
func BearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != "" && !strings.ContainsAny(token, " \t")
}
