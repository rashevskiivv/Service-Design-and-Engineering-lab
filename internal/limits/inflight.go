package limits

import "sync"

// KeyInflight caps concurrent requests per key, queued ones included, so one
// person cannot hold many global slots.
type KeyInflight struct {
	mu sync.Mutex
	n  map[int64]int
}

// NewKeyInflight returns an empty counter.
func NewKeyInflight() *KeyInflight { return &KeyInflight{n: map[int64]int{}} }

// TryAcquire takes a slot for keyID if fewer than limit are in use (limit 0 =
// unlimited). The returned release is idempotent.
func (k *KeyInflight) TryAcquire(keyID int64, limit int) (release func(), ok bool) {
	if limit <= 0 {
		return func() {}, true
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.n[keyID] >= limit {
		return nil, false
	}
	k.n[keyID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			k.mu.Lock()
			defer k.mu.Unlock()
			if k.n[keyID]--; k.n[keyID] <= 0 {
				delete(k.n, keyID)
			}
		})
	}, true
}
