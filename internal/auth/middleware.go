package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"local-generative-ai/internal/config"
	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/store"
)

// KeyLookup finds a key that is active at now; anything else is
// store.ErrNotFound.
type KeyLookup interface {
	KeyByHash(ctx context.Context, hash [32]byte, now time.Time) (store.Key, error)
}

// Principal is an authenticated key with its effective limits (per-key
// overrides applied over the env defaults; 0 = unlimited).
type Principal struct {
	KeyID       int64
	RPM         int
	Burst       int
	MaxInflight int
	DailyQuota  int64
}

type principalKey struct{}

// PrincipalFrom returns the principal RequireKey stored in ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// ErrUnauthorized is the one 401 for a missing, malformed, unknown, revoked or
// expired key: callers cannot tell these apart.
func ErrUnauthorized() *oai.Error {
	return oai.NewError(http.StatusUnauthorized, oai.TypeAuthentication, oai.CodeInvalidAPIKey, "",
		"invalid or missing API key; send it as Authorization: Bearer lgai_...")
}

// RequireKey authenticates the user key before anything reads the body. There
// is no cache, so a revocation applies to the very next request.
func RequireKey(l KeyLookup, defaults config.Limits, now func() time.Time, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := BearerToken(r)
			if !ok || !WellFormed(token) {
				oai.WriteError(w, ErrUnauthorized())
				return
			}
			k, err := l.KeyByHash(r.Context(), Hash(token), now())
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					log.Error("key lookup failed", "err", err)
					oai.WriteError(w, oai.Internal())
					return
				}
				oai.WriteError(w, ErrUnauthorized())
				return
			}
			p := Principal{KeyID: k.ID, RPM: defaults.RateRPM, Burst: defaults.RateBurst,
				MaxInflight: defaults.KeyMaxInflight, DailyQuota: defaults.TokenQuotaDaily}
			if k.RPMLimit != nil {
				p.RPM = int(*k.RPMLimit)
			}
			if k.MaxInflight != nil {
				p.MaxInflight = int(*k.MaxInflight)
			}
			if k.DailyTokenQuota != nil {
				p.DailyQuota = *k.DailyTokenQuota
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// RequireAdmin guards the whole admin mux (fail-closed). A missing token is
// 401; a wrong one, including a user key, is 403. Both sides are hashed before
// the constant-time compare, which equalises lengths.
func RequireAdmin(token string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			given, ok := BearerToken(r)
			if !ok {
				oai.WriteError(w, oai.NewError(http.StatusUnauthorized, oai.TypeAuthentication,
					oai.CodeMissingAdminToken, "", "missing admin token"))
				return
			}
			got := sha256.Sum256([]byte(given))
			if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				oai.WriteError(w, oai.NewError(http.StatusForbidden, oai.TypePermission,
					oai.CodeForbidden, "", "invalid admin token"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
