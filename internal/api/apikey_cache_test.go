package api

import (
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/auth"
)

// TestAPIKeyCacheKeyedOnPrefixAndSecret locks in the C1 fix: the cache must
// not hand back a principal when the presented secret differs, even if the
// prefix matches a previously-cached entry. Pre-fix, an attacker who learned
// the prefix (it leaks in audit logs, error envelopes, server logs) could
// authenticate with any secret during the 30s TTL after a legitimate use.
func TestAPIKeyCacheKeyedOnPrefixAndSecret(t *testing.T) {
	c := newAPIKeyCache(30 * time.Second)
	prefix := "teo_ci_abcdef"
	good := prefix + ".good-secret"
	bad := prefix + ".attacker-secret"

	p := &auth.Principal{APIKeyID: "id-1", IsAPIKey: true}
	c.Put(cacheKey(prefix, good), p)

	if _, ok := c.Get(cacheKey(prefix, good)); !ok {
		t.Fatal("legitimate (prefix, secret) lookup should hit cache")
	}
	if _, ok := c.Get(cacheKey(prefix, bad)); ok {
		t.Fatal("attacker (prefix, wrong-secret) lookup must NOT hit cache")
	}
	// Also verify the prefix alone doesn't match — the bug was a Get(prefix).
	if _, ok := c.Get(prefix); ok {
		t.Fatal("bare prefix lookup must not hit cache")
	}
}

func TestAPIKeyCacheTTLExpiry(t *testing.T) {
	c := newAPIKeyCache(10 * time.Millisecond)
	key := cacheKey("teo_ci_x", "teo_ci_x.s")
	c.Put(key, &auth.Principal{APIKeyID: "id"})
	if _, ok := c.Get(key); !ok {
		t.Fatal("entry should be present immediately after Put")
	}
	time.Sleep(15 * time.Millisecond)
	if _, ok := c.Get(key); ok {
		t.Fatal("entry should be expired past TTL")
	}
}
