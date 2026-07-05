// observer_decisions_test.go — tests for the DecisionsObserver ResponseObserver.
//
// Covers the three spec scenarios from the change's spec delta:
//   - "Decision markers are persisted to Store"
//   - "DecisionsObserver is non-aborting on Store failures"
//   - "DecisionsObserver does not panic on malformed responses"
package sdd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tau "github.com/coevin/tau/pkg/tau"
)

func TestDecisionsObserver_PersistsExtractedMarkers(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	obs := NewDecisionsObserver(store, "spec-driven-development")
	obs.SetTimeNow(func() time.Time { return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC) })

	resp := &tau.Response{Content: []tau.ContentBlock{
		tau.TextContent{Text: "some preamble\n**Decision 1**: pick adapter types\nmore text\nD2. split into three interfaces\n"},
		tau.TextContent{Text: "Decision: use sentinel errors\n"},
	}}
	if err := obs.ObserveResponse(context.Background(), nil, resp); err != nil {
		t.Fatalf("ObserveResponse: %v", err)
	}

	entries, err := store.Query(context.Background(), tau.Query{Limit: 100})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("Query len = %d, want 3 (%v)", len(entries), entries)
	}
	for _, e := range entries {
		if e.Source != "sdd" {
			t.Errorf("entry Source = %q, want \"sdd\"", e.Source)
		}
		// Tags must include "sdd", "decision", and the subsystem tag.
		for _, want := range []string{"sdd", "decision", "spec-driven-development"} {
			found := false
			for _, tag := range e.Tags {
				if tag == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("entry %q missing tag %q; tags=%v", e.ID, want, e.Tags)
			}
		}
		// ID prefix.
		if !strings.HasPrefix(e.ID, "sdd-decision-") {
			t.Errorf("entry ID = %q, want prefix sdd-decision-", e.ID)
		}
		// Timestamp came from SetTimeNow.
		want := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
		if !e.Timestamp.Equal(want) {
			t.Errorf("entry Timestamp = %v, want %v", e.Timestamp, want)
		}
	}
}

func TestDecisionsObserver_NonAbortingOnStorePutFailure(t *testing.T) {
	store := &failingPutStore{err: errors.New("disk full")}
	obs := NewDecisionsObserver(store, "")

	resp := &tau.Response{Content: []tau.ContentBlock{
		tau.TextContent{Text: "**Decision 1**: still parses fine\n"},
	}}
	if err := obs.ObserveResponse(context.Background(), nil, resp); err != nil {
		t.Errorf("ObserveResponse: want nil for store failure, got %v", err)
	}
}

func TestDecisionsObserver_NonAbortingOnNilStore(t *testing.T) {
	obs := NewDecisionsObserver(nil, "")
	resp := &tau.Response{Content: []tau.ContentBlock{
		tau.TextContent{Text: "**Decision 1**: nothing persists\n"},
	}}
	if err := obs.ObserveResponse(context.Background(), nil, resp); err != nil {
		t.Errorf("ObserveResponse: want nil for nil store, got %v", err)
	}
}

func TestDecisionsObserver_NoPanicOnMalformedContent(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	obs := NewDecisionsObserver(store, "")

	// A block whose Type is unexpected (TextContent assertion fails)
	// should be skipped, not panic.
	resp := &tau.Response{Content: []tau.ContentBlock{
		tau.ImageContent{Data: "ZGF0YQ==", MimeType: "image/png"},
		tau.TextContent{Text: "Decision: ok\n"},
	}}
	if err := obs.ObserveResponse(context.Background(), nil, resp); err != nil {
		t.Errorf("ObserveResponse: want nil, got %v", err)
	}
	entries, _ := store.Query(context.Background(), tau.Query{Limit: 100})
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (ImageContent skipped), got %d", len(entries))
	}
}

// TestDecisionsObserver_SyntheticPanicRecovered exercises the
// `defer recover()` branch in safeScan directly. scanDecisions itself
// cannot panic on any well-typed input, so we inject a scanner that
// panics synthetically (simulating a malformed Content block from a
// future runtime revision). Acceptance per task 7.2: the observer
// leaves req/resp unchanged and returns nil.
func TestDecisionsObserver_SyntheticPanicRecovered(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	obs := NewDecisionsObserver(store, "")

	panicked := false
	obs.SetScanner(func(resp *tau.Response) []string {
		panicked = true
		panic("synthetic malformed Content block")
	})

	req := &tau.Request{System: []tau.ContentBlock{tau.TextContent{Text: "system prompt"}}}
	resp := &tau.Response{Content: []tau.ContentBlock{
		tau.TextContent{Text: "**Decision 1**: would normally persist\n"},
	}}
	systemBefore := req.System

	if err := obs.ObserveResponse(context.Background(), req, resp); err != nil {
		t.Errorf("ObserveResponse after panic: want nil, got %v", err)
	}
	if !panicked {
		t.Fatalf("injected scanner was not invoked; recovery branch not exercised")
	}
	// req must be unchanged.
	if len(req.System) != len(systemBefore) {
		t.Errorf("req.System mutated by observer: before=%v, after=%v", systemBefore, req.System)
	}
	// Store must be empty (panic aborted the scan before any Put).
	entries, _ := store.Query(context.Background(), tau.Query{Limit: 100})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after recovered panic, got %d", len(entries))
	}
}

func TestDecisionsObserver_NilResponseIsSafe(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	obs := NewDecisionsObserver(store, "")
	if err := obs.ObserveResponse(context.Background(), nil, nil); err != nil {
		t.Errorf("ObserveResponse(nil resp): want nil, got %v", err)
	}
}

func TestDecisionsObserver_ConcurrentSafe(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	obs := NewDecisionsObserver(store, "")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := &tau.Response{Content: []tau.ContentBlock{
				tau.TextContent{Text: "D1. concurrent call\n"},
			}}
			_ = obs.ObserveResponse(context.Background(), nil, resp)
		}()
	}
	wg.Wait()
}

func TestScanDecisions_AllThreeForms(t *testing.T) {
	resp := &tau.Response{Content: []tau.ContentBlock{
		tau.TextContent{Text: `preamble

**Decision 1**: pick adapter types
D2. split into three interfaces
Decision: use sentinel errors
`},
	}}
	got := scanDecisions(resp)
	if len(got) != 3 {
		t.Fatalf("scanDecisions len = %d, want 3 (%v)", len(got), got)
	}
	if !strings.Contains(got[0], "1:") || !strings.Contains(got[0], "adapter") {
		t.Errorf("got[0] = %q, want '<id>: adapter...'", got[0])
	}
	if !strings.Contains(got[1], "D2.") {
		t.Errorf("got[1] = %q", got[1])
	}
	if !strings.Contains(got[2], "sentinel") {
		t.Errorf("got[2] = %q", got[2])
	}
}

func TestDecisionID_Stable(t *testing.T) {
	a := decisionID("some text")
	b := decisionID("some text")
	if a != b {
		t.Errorf("decisionID not stable: %q vs %q", a, b)
	}
	c := decisionID("different text")
	if a == c {
		t.Errorf("decisionID collision between different texts")
	}
	// Length should be 16 (first 16 hex chars of sha256).
	if len(a) != 16 {
		t.Errorf("decisionID len = %d, want 16", len(a))
	}
}

// failingPutStore is a tau.Store whose Put always returns the supplied
// error. Query returns an empty slice; Close is a no-op.
type failingPutStore struct{ err error }

func (s *failingPutStore) Put(context.Context, tau.Entry) error       { return s.err }
func (s *failingPutStore) Query(context.Context, tau.Query) ([]tau.Entry, error) {
	return nil, nil
}
func (s *failingPutStore) Close() error { return nil }

// Compile-time assertion: *DecisionsObserver satisfies tau.ResponseObserver.
var _ tau.ResponseObserver = (*DecisionsObserver)(nil)
