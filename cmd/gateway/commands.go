package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"local-generative-ai/internal/auth"
	"local-generative-ai/internal/config"
	"local-generative-ai/internal/store"
)

// healthcheck GETs /healthz on the local public listener (distroless images
// have no curl). It returns the process exit code.
func healthcheck(addr string) int {
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		_, errL := fmt.Fprintln(os.Stderr, "healthcheck: LGAI_ADDR must be host:port")
		if errL != nil {
			slog.Error("healthcheck: LGAI_ADDR must be host:port", "error", errL)
		}
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		_, errL := fmt.Fprintln(os.Stderr, "healthcheck:", err)
		if errL != nil {
			slog.Error("healthcheck get", "error", errL)
		}
		return 1
	}
	defer func(Body io.ReadCloser) {
		errL := Body.Close()
		if errL != nil {
			slog.Error("healthcheck: body close", "error", errL)
		}
	}(resp.Body)
	if resp.StatusCode != http.StatusOK {
		_, errL := fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		if errL != nil {
			slog.Error("healthcheck: status", "error", errL)
		}
		return 1
	}
	return 0
}

// genSecret prints a random 32-byte base64 secret that passes the secret rules.
func genSecret(w io.Writer) int {
	for {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			_, errL := fmt.Fprintln(os.Stderr, "gen-secret:", err)
			if errL != nil {
				slog.Error("gen-secret", "error", errL)
			}
			return 1
		}
		s := base64.StdEncoding.EncodeToString(b[:])
		if config.ValidateSecret(s) == nil {
			_, errL := fmt.Fprintln(w, s)
			if errL != nil {
				slog.Error("gen-secret s", "error", errL)
			}
			return 0
		}
	}
}

// genKeys mints keys offline for LGAI_KEYS_FILE (DECISIONS D2, security #26):
// the plaintext list (name,key) for handing out, and a hashes-only file for the
// gateway. Both are created with mode 0600 and never overwritten.
func genKeys(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("gen-keys", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 0, "number of keys (1-1000)")
	prefix := fs.String("prefix", "", "name prefix, e.g. lab- (names become lab-01, lab-02, ...)")
	expires := fs.String("expires", "", "optional expiry, RFC 3339, e.g. 2026-10-14T18:00:00Z")
	out := fs.String("out", "keys.csv", "plaintext output: name,key (keep it private)")
	hashes := fs.String("hashes", "keys.hashes", "hashes output for LGAI_KEYS_FILE")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(format string, a ...any) int {
		_, err := fmt.Fprintf(stderr, "gen-keys: "+format+"\n", a...)
		if err != nil {
			slog.Error("gen-keys", "error", err)
		}
		return 1
	}
	if *n < 1 || *n > 1000 || *prefix == "" || strings.ContainsAny(*prefix, ",\n") {
		return fail("need -n 1..1000 and a -prefix without commas")
	}
	var exp *time.Time
	if *expires != "" {
		t, err := time.Parse(time.RFC3339, *expires)
		if err != nil || !t.After(time.Now()) {
			return fail("-expires must be a future RFC 3339 time")
		}
		t = t.UTC()
		exp = &t
	}

	var plain, hashed strings.Builder
	plain.WriteString("name,key\n")
	hashed.WriteString(store.KeysFileHeader + "\n")
	width := max(2, len(strconv.Itoa(*n)))
	for i := 1; i <= *n; i++ {
		key, hash, keyPrefix, err := auth.Generate()
		if err != nil {
			return fail("%v", err)
		}
		name := fmt.Sprintf("%s%0*d", *prefix, width, i)
		plain.WriteString(name + "," + key + "\n")
		hashed.WriteString(store.KeysFileLine(store.NewKey{Name: name, Prefix: keyPrefix, Hash: hash, ExpiresAt: exp}) + "\n")
	}
	if err := writeNew(*out, plain.String()); err != nil {
		return fail("%v", err)
	}
	if err := writeNew(*hashes, hashed.String()); err != nil {
		return fail("%v", err)
	}
	_, err := fmt.Fprintf(stderr, "wrote %d keys: %s (plaintext, hand out and then delete) and %s (set LGAI_KEYS_FILE to it)\n", *n, *out, *hashes)
	if err != nil {
		slog.Error("fmt.Fprintf: gen-keys", "error", err)
	}
	return 0
}

// writeNew creates path with mode 0600, failing if it already exists.
func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
