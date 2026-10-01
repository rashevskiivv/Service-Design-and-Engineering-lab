package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Key is an API key row as the admin API shows it. It never carries the
// plaintext key or its hash.
type Key struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Prefix          string     `json:"key_prefix"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       *time.Time `json:"expires_at"`
	RevokedAt       *time.Time `json:"revoked_at"`
	RPMLimit        *int64     `json:"rpm_limit"`
	DailyTokenQuota *int64     `json:"daily_token_quota"`
	MaxInflight     *int64     `json:"max_inflight"`
	LastUsedAt      *time.Time `json:"last_used_at"`
}

// NewKey is a key to insert. Overrides: nil = env default, 0 = unlimited.
type NewKey struct {
	Name            string
	Prefix          string
	Hash            [32]byte
	ExpiresAt       *time.Time
	RPMLimit        *int64
	DailyTokenQuota *int64
	MaxInflight     *int64
}

const keyColumns = `id, name, key_prefix, created_at, expires_at, revoked_at,
	rpm_limit, daily_token_quota, max_inflight,
	(SELECT MAX(ts) FROM usage u WHERE u.key_id = api_keys.id)`

type scanner interface{ Scan(dest ...any) error }

func scanKey(r scanner) (Key, error) {
	var k Key
	var created int64
	var expires, revoked, rpm, quota, inflight, last sql.NullInt64
	if err := r.Scan(&k.ID, &k.Name, &k.Prefix, &created, &expires, &revoked, &rpm, &quota, &inflight, &last); err != nil {
		return Key{}, err
	}
	k.CreatedAt = fromMS(created)
	k.ExpiresAt, k.RevokedAt, k.LastUsedAt = nullTime(expires), nullTime(revoked), nullTime(last)
	k.RPMLimit, k.DailyTokenQuota, k.MaxInflight = nullInt(rpm), nullInt(quota), nullInt(inflight)
	return k, nil
}

// CreateKeys inserts keys in one transaction and returns them in order.
func (s *Store) CreateKeys(ctx context.Context, keys []NewKey, now time.Time) ([]Key, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := make([]Key, 0, len(keys))
	for _, k := range keys {
		row := tx.QueryRowContext(ctx, `INSERT INTO api_keys
			(name, key_hash, key_prefix, created_at, expires_at, rpm_limit, daily_token_quota, max_inflight)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+keyColumns,
			k.Name, k.Hash[:], k.Prefix, ms(now), nullMS(k.ExpiresAt), k.RPMLimit, k.DailyTokenQuota, k.MaxInflight)
		created, err := scanKey(row)
		if err != nil {
			return nil, err
		}
		out = append(out, created)
	}
	return out, tx.Commit()
}

// ImportKeys inserts pre-minted key hashes (LGAI_KEYS_FILE). A hash that is
// already present is left untouched, so a revoked key is never revived. It
// returns how many keys were added.
func (s *Store) ImportKeys(ctx context.Context, keys []NewKey, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	added := 0
	for _, k := range keys {
		res, err := tx.ExecContext(ctx, `INSERT INTO api_keys (name, key_hash, key_prefix, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (key_hash) DO NOTHING`,
			k.Name, k.Hash[:], k.Prefix, ms(now), nullMS(k.ExpiresAt))
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		added += int(n)
	}
	return added, tx.Commit()
}

// KeyByHash returns the key with this hash if it is active at now: not revoked
// and not expired. Anything else is ErrNotFound, so callers cannot tell an
// unknown key from a revoked or expired one.
func (s *Store) KeyByHash(ctx context.Context, hash [32]byte, now time.Time) (Key, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, key_prefix, created_at, expires_at, revoked_at,
		rpm_limit, daily_token_quota, max_inflight, NULL
		FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		hash[:], ms(now))
	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	return k, err
}

// GetKey returns one key by id.
func (s *Store) GetKey(ctx context.Context, id int64) (Key, error) {
	k, err := scanKey(s.db.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	return k, err
}

// ListKeys returns every key (active, expired and revoked) ordered by id.
func (s *Store) ListKeys(ctx context.Context) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+keyColumns+` FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RevokeKey revokes one key. It is idempotent: the first revocation time is kept.
func (s *Store) RevokeKey(ctx context.Context, id int64, now time.Time) (Key, error) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = COALESCE(revoked_at, MAX(?, created_at)) WHERE id = ?`, ms(now), id); err != nil {
		return Key{}, err
	}
	return s.GetKey(ctx, id)
}

// RevokeKeys revokes every active key whose name starts with namePrefix, or
// every active key when all is true. It returns the number revoked.
func (s *Store) RevokeKeys(ctx context.Context, namePrefix string, all bool, now time.Time) (int64, error) {
	if !all && namePrefix == "" {
		return 0, errors.New("store: empty name prefix")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at = MAX(?1, created_at)
		WHERE revoked_at IS NULL AND (?2 OR substr(name, 1, length(?3)) = ?3)`, ms(now), all, namePrefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
