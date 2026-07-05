// memstore_test.go — minimal in-memory tau.Store for SDD plugin tests.
//
// Mirrors the headroom plugin's memstore_test.go pattern: Put is
// idempotent on duplicate IDs, Query supports keyword + tag filters,
// Close flips a closed bit. Safe for concurrent use.
package sdd

import (
	"context"
	"strings"
	"sync"

	tau "github.com/coevin/tau/pkg/tau"
)

type memStore struct {
	mu      sync.RWMutex
	entries []tau.Entry
	closed  bool
}

func newMemStore() *memStore { return &memStore{} }

func (m *memStore) Put(_ context.Context, e tau.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return tau.ErrStoreClosed
	}
	for i := range m.entries {
		if m.entries[i].ID == e.ID {
			m.entries[i] = e
			return nil
		}
	}
	m.entries = append(m.entries, e)
	return nil
}

func (m *memStore) Query(_ context.Context, q tau.Query) ([]tau.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, tau.ErrStoreClosed
	}
	var out []tau.Entry
	for _, e := range m.entries {
		if q.KeywordQuery != "" {
			if !strings.Contains(strings.ToLower(e.Text), strings.ToLower(q.KeywordQuery)) &&
				e.ID != q.KeywordQuery {
				continue
			}
		}
		if len(q.TagsQuery) > 0 && !hasAllTags(e.Tags, q.TagsQuery) {
			continue
		}
		out = append(out, e)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (m *memStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func hasAllTags(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
