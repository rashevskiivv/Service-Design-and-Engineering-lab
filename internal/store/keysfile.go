package store

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// KeysFileHeader is the comment line that starts a keys file.
const KeysFileHeader = "# name,key_prefix,sha256_hex[,expires_at RFC3339]"

// ParseKeysFile reads an LGAI_KEYS_FILE: one key per line as
// "name,key_prefix,sha256_hex[,expires_at]", with '#' comments and blank lines
// ignored. It holds hashes only, never plaintext keys. Keys that have already
// expired at now are skipped and counted.
func ParseKeysFile(r io.Reader, now time.Time) (keys []NewKey, expired int, err error) {
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		k, err := parseKeysFileLine(text)
		if err != nil {
			return nil, 0, fmt.Errorf("keys file line %d: %w", line, err)
		}
		if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
			expired++
			continue
		}
		keys = append(keys, k)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("keys file: %w", err)
	}
	return keys, expired, nil
}

func parseKeysFileLine(text string) (NewKey, error) {
	parts := strings.Split(text, ",")
	if len(parts) < 3 || len(parts) > 4 {
		return NewKey{}, fmt.Errorf("want name,key_prefix,sha256_hex[,expires_at]")
	}
	var k NewKey
	k.Name, k.Prefix = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if n := utf8.RuneCountInString(k.Name); n < 1 || n > 100 {
		return NewKey{}, fmt.Errorf("name must be 1-100 characters")
	}
	if len(k.Prefix) != 12 || !strings.HasPrefix(k.Prefix, "lgai_") {
		return NewKey{}, fmt.Errorf("key_prefix must be the first 12 characters of an lgai_ key")
	}
	h, err := hex.DecodeString(strings.TrimSpace(parts[2]))
	if err != nil || len(h) != 32 {
		return NewKey{}, fmt.Errorf("sha256_hex must be 64 hex characters")
	}
	copy(k.Hash[:], h)
	if len(parts) == 4 && strings.TrimSpace(parts[3]) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[3]))
		if err != nil {
			return NewKey{}, fmt.Errorf("expires_at must be RFC 3339")
		}
		t = t.UTC()
		k.ExpiresAt = &t
	}
	return k, nil
}

// KeysFileLine formats k as one keys-file line.
func KeysFileLine(k NewKey) string {
	exp := ""
	if k.ExpiresAt != nil {
		exp = "," + k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return k.Name + "," + k.Prefix + "," + hex.EncodeToString(k.Hash[:]) + exp
}
