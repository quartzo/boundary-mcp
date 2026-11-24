package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

const (
	cacheVersion    = "1"
	defaultCacheMax = 512
)

// Cached wraps a Judge with an in-memory cache keyed by the canonical state,
// the rule set and the namespace (normally the model ID).
type Cached struct {
	inner     Judge
	namespace string
	max       int

	mu      sync.Mutex
	entries map[string]*Assessment
	order   []string
}

// NewCached returns a caching judge.
func NewCached(inner Judge, namespace string) *Cached {
	if namespace == "" {
		namespace = "default"
	}
	return &Cached{
		inner:     inner,
		namespace: namespace,
		max:       defaultCacheMax,
		entries:   make(map[string]*Assessment),
	}
}

// Evaluate returns a cached assessment when the canonical state is unchanged.
func (c *Cached) Evaluate(ctx context.Context, in Input) (*Assessment, error) {
	key := c.key(in)

	c.mu.Lock()
	if cached, ok := c.entries[key]; ok {
		copied := *cached
		copied.Cached = true
		c.mu.Unlock()
		return &copied, nil
	}
	c.mu.Unlock()

	assessment, err := c.inner.Evaluate(ctx, in)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		if len(c.entries) >= c.max && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		copied := *assessment
		c.entries[key] = &copied
		c.order = append(c.order, key)
	}
	return assessment, nil
}

func (c *Cached) key(in Input) string {
	payload, _ := json.Marshal(struct {
		Version   string              `json:"version"`
		Namespace string              `json:"namespace"`
		State     state               `json:"state"`
		Questions map[string]question `json:"questions"`
	}{
		Version:   cacheVersion,
		Namespace: c.namespace,
		State:     buildState(in),
		Questions: questions(),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
