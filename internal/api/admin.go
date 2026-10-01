package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"local-generative-ai/internal/auth"
	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/store"
)

const (
	maxBatch      = 200
	maxNameLen    = 100
	maxPrefixLen  = 90
	adminLogEvent = "admin"
)

// keyCreated is a key plus its plaintext, shown exactly once.
type keyCreated struct {
	store.Key
	Plaintext string `json:"key"`
}

type keyCreateRequest struct {
	Name            string  `json:"name"`
	RPMLimit        *int64  `json:"rpm_limit"`
	DailyTokenQuota *int64  `json:"daily_token_quota"`
	MaxInflight     *int64  `json:"max_inflight"`
	ExpiresAt       *string `json:"expires_at"`
}

type batchRequest struct {
	Count      int     `json:"count"`
	NamePrefix string  `json:"name_prefix"`
	ExpiresAt  *string `json:"expires_at"`
}

type revokeRequest struct {
	NamePrefix string `json:"name_prefix"`
	All        bool   `json:"all"`
}

type maintenanceRequest struct {
	Enabled *bool `json:"enabled"`
}

// decodeAdmin reads and strictly decodes an admin body; mass assignment of
// id, key, key_hash, created_at, … fails as unknown_field.
func (s *Server) decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	body, e := s.readBody(w, r)
	if e == nil {
		e = oai.DecodeStrict(body, v)
	}
	if e != nil {
		oai.WriteError(w, e)
		return false
	}
	return true
}

// createKey serves POST /admin/keys.
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var req keyCreateRequest
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	if e := checkName("name", req.Name, maxNameLen); e != nil {
		oai.WriteError(w, e)
		return
	}
	for _, o := range []struct {
		param string
		v     *int64
	}{{"rpm_limit", req.RPMLimit}, {"daily_token_quota", req.DailyTokenQuota}, {"max_inflight", req.MaxInflight}} {
		if o.v != nil && *o.v < 0 {
			oai.WriteError(w, oai.Invalid(o.param, "%s must be >= 0 (0 = unlimited) or null", o.param))
			return
		}
	}
	expires, e := s.expiry(req.ExpiresAt)
	if e != nil {
		oai.WriteError(w, e)
		return
	}
	nk, plain, err := newKey(req.Name, expires)
	if err != nil {
		s.adminFailed(w, "create_key", err)
		return
	}
	nk.RPMLimit, nk.DailyTokenQuota, nk.MaxInflight = req.RPMLimit, req.DailyTokenQuota, req.MaxInflight
	keys, err := s.store.CreateKeys(r.Context(), []store.NewKey{nk}, s.now())
	if err != nil {
		s.adminFailed(w, "create_key", err)
		return
	}
	s.log.Info(adminLogEvent, "action", "create_key", "key_id", keys[0].ID)
	oai.WriteJSON(w, http.StatusCreated, keyCreated{Key: keys[0], Plaintext: plain})
}

// createKeyBatch serves POST /admin/keys/batch: count keys named
// name_prefix + a zero-padded index, returned with their plaintext once.
func (s *Server) createKeyBatch(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	if req.Count < 1 || req.Count > maxBatch {
		oai.WriteError(w, oai.Invalid("count", "count must be 1-%d", maxBatch))
		return
	}
	if e := checkName("name_prefix", req.NamePrefix, maxPrefixLen); e != nil {
		oai.WriteError(w, e)
		return
	}
	expires, e := s.expiry(req.ExpiresAt)
	if e != nil {
		oai.WriteError(w, e)
		return
	}
	width := max(2, len(strconv.Itoa(req.Count)))
	news := make([]store.NewKey, req.Count)
	plains := make([]string, req.Count)
	for i := range news {
		var err error
		news[i], plains[i], err = newKey(fmt.Sprintf("%s%0*d", req.NamePrefix, width, i+1), expires)
		if err != nil {
			s.adminFailed(w, "create_keys", err)
			return
		}
	}
	keys, err := s.store.CreateKeys(r.Context(), news, s.now())
	if err != nil {
		s.adminFailed(w, "create_keys", err)
		return
	}
	out := make([]keyCreated, len(keys))
	for i, k := range keys {
		out[i] = keyCreated{Key: k, Plaintext: plains[i]}
	}
	s.log.Info(adminLogEvent, "action", "create_keys", "count", len(keys))
	oai.WriteJSON(w, http.StatusCreated, map[string]any{"object": "list", "data": out})
}

// listKeys serves GET /admin/keys.
func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListKeys(r.Context())
	if err != nil {
		s.adminFailed(w, "list_keys", err)
		return
	}
	oai.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": keys})
}

// getKey serves GET /admin/keys/{id}.
func (s *Server) getKey(w http.ResponseWriter, r *http.Request) {
	id, ok := keyID(w, r)
	if !ok {
		return
	}
	k, err := s.store.GetKey(r.Context(), id)
	if s.keyResult(w, "get_key", id, err) {
		oai.WriteJSON(w, http.StatusOK, k)
	}
}

// revokeKey serves DELETE /admin/keys/{id} (idempotent).
func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	id, ok := keyID(w, r)
	if !ok {
		return
	}
	k, err := s.store.RevokeKey(r.Context(), id, s.now())
	if s.keyResult(w, "revoke_key", id, err) {
		s.log.Info(adminLogEvent, "action", "revoke_key", "key_id", id)
		oai.WriteJSON(w, http.StatusOK, k)
	}
}

// revokeKeys serves POST /admin/keys/revoke: {"name_prefix": "lab-"} or {"all": true}.
func (s *Server) revokeKeys(w http.ResponseWriter, r *http.Request) {
	var req revokeRequest
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	if (req.NamePrefix == "") == !req.All {
		oai.WriteError(w, oai.Invalid("name_prefix", `send either {"name_prefix": "..."} or {"all": true}`))
		return
	}
	n, err := s.store.RevokeKeys(r.Context(), req.NamePrefix, req.All, s.now())
	if err != nil {
		s.adminFailed(w, "revoke_keys", err)
		return
	}
	s.log.Info(adminLogEvent, "action", "revoke_keys", "all", req.All, "count", n)
	oai.WriteJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}

// keyUsage serves GET /admin/keys/{id}/usage?since=&until= (RFC 3339; default
// today 00:00 UTC until now).
func (s *Server) keyUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := keyID(w, r)
	if !ok {
		return
	}
	now := s.now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	until := now
	for _, q := range []struct {
		name string
		dst  *time.Time
	}{{"since", &since}, {"until", &until}} {
		if v := r.URL.Query().Get(q.name); v != "" {
			// An unescaped "+02:00" offset arrives as " 02:00"; RFC 3339 has no spaces.
			t, err := time.Parse(time.RFC3339, strings.ReplaceAll(v, " ", "+"))
			if err != nil {
				oai.WriteError(w, oai.Invalid(q.name, "%s must be an RFC 3339 time", q.name))
				return
			}
			*q.dst = t
		}
	}
	if !since.Before(until) {
		oai.WriteError(w, oai.Invalid("since", "since must be before until"))
		return
	}
	u, err := s.store.KeyUsage(r.Context(), id, since, until)
	if s.keyResult(w, "key_usage", id, err) {
		oai.WriteJSON(w, http.StatusOK, u)
	}
}

// getMaintenance serves GET /admin/maintenance.
func (s *Server) getMaintenance(w http.ResponseWriter, _ *http.Request) {
	oai.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": s.maintenance.Load()})
}

// setMaintenance serves PUT /admin/maintenance {"enabled": bool}. While on,
// generation returns 503 maintenance; health, models and admin keep working.
func (s *Server) setMaintenance(w http.ResponseWriter, r *http.Request) {
	var req maintenanceRequest
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		oai.WriteError(w, oai.BadRequest(oai.CodeMissingField, "enabled", "enabled is required"))
		return
	}
	s.maintenance.Store(*req.Enabled)
	s.log.Info(adminLogEvent, "action", "maintenance", "enabled", *req.Enabled)
	oai.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": *req.Enabled})
}

// expiry resolves an optional RFC 3339 expires_at; when absent, LGAI_KEY_TTL
// (if > 0) sets it.
func (s *Server) expiry(v *string) (*time.Time, *oai.Error) {
	now := s.now()
	if v == nil {
		if s.cfg.KeyTTL <= 0 {
			return nil, nil
		}
		t := now.Add(s.cfg.KeyTTL).UTC()
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339, *v)
	if err != nil {
		return nil, oai.Invalid("expires_at", "expires_at must be an RFC 3339 time or null")
	}
	if !t.After(now) {
		return nil, oai.Invalid("expires_at", "expires_at must be in the future")
	}
	t = t.UTC()
	return &t, nil
}

func newKey(name string, expires *time.Time) (store.NewKey, string, error) {
	plain, hash, prefix, err := auth.Generate()
	if err != nil {
		return store.NewKey{}, "", err
	}
	return store.NewKey{Name: name, Prefix: prefix, Hash: hash, ExpiresAt: expires}, plain, nil
}

func checkName(param, v string, maxLen int) *oai.Error {
	n := utf8.RuneCountInString(v)
	if n < 1 || n > maxLen || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return oai.Invalid(param, "%s must be 1-%d printable characters (use seat or role labels, not real names)", param, maxLen)
	}
	return nil
}

func keyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		oai.WriteError(w, keyNotFound())
		return 0, false
	}
	return id, true
}

func keyNotFound() *oai.Error {
	return oai.NewError(http.StatusNotFound, oai.TypeNotFound, oai.CodeKeyNotFound, "id", "key not found")
}

// keyResult writes the error for err (if any) and reports whether to go on.
func (s *Server) keyResult(w http.ResponseWriter, action string, id int64, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotFound):
		oai.WriteError(w, keyNotFound())
	default:
		s.log.Error("admin action failed", "action", action, "key_id", id, "err", err)
		oai.WriteError(w, oai.Internal())
	}
	return false
}

func (s *Server) adminFailed(w http.ResponseWriter, action string, err error) {
	s.log.Error("admin action failed", "action", action, "err", err)
	oai.WriteError(w, oai.Internal())
}
