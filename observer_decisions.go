// observer_decisions.go — the DecisionsObserver ResponseObserver.
//
// Implements design.md D7: a ResponseObserver that mines assistant
// responses for decision blocks (`**Decision <id>**:`, `D<n>.`, and
// `Decision:`) and writes the extracted text to the injected tau.Store
// with Tags ["sdd", "decision", <subsystem>] and Source "sdd".
//
// Per pkg/tau/middleware.go:64-66 a non-nil return is logged but does
// not abort the turn. The observer treats every error as
// non-fatal: store.Put failures are dropped silently (the runtime logs
// the nil return), and panicking from a malformed Content block is
// recovered so the agent loop never sees a panic propagate from this
// observer.
package sdd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"

	tau "github.com/taucentral/tau/pkg/tau"
)

// DecisionsObserver is a ResponseObserver that scans each completed
// response for decision markers and writes extracted decisions to a
// tau.Store. The observer is safe for concurrent use across goroutines.
type DecisionsObserver struct {
	store        tau.Store
	tagSubsystem string

	// timeNow is overridable for tests so the timestamp can be made
	// deterministic. Production callers leave it zero, in which case
	// the observer uses time.Now().UTC().
	timeNow func() time.Time

	// scanner is overridable for tests so the panic-recovery branch in
	// safeScan can be exercised against a synthetic panicking scan.
	// Production callers leave it nil, in which case the observer uses
	// the package-level scanDecisions function.
	scanner func(*tau.Response) []string

	mu sync.Mutex
}

// NewDecisionsObserver returns a DecisionsObserver bound to store.
// tagSubsystem is the third tag added to every Entry ("sdd" and
// "decision" are the first two). Pass "" to omit the subsystem tag.
func NewDecisionsObserver(store tau.Store, tagSubsystem string) *DecisionsObserver {
	return &DecisionsObserver{
		store:        store,
		tagSubsystem: tagSubsystem,
		timeNow:      time.Now().UTC,
	}
}

// ObserveResponse satisfies tau.ResponseObserver. The observer is
// non-aborting: any store.Put failure or scan panic is recovered and
// logged (effectively dropped). Returns ctx.Err() when the context is
// cancelled; otherwise nil.
func (o *DecisionsObserver) ObserveResponse(ctx context.Context, req *tau.Request, resp *tau.Response, streamErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if resp == nil {
		return nil
	}

	// Recover from any panic in the marker scan so a malformed
	// Content block cannot abort the runtime.
	decisions := o.safeScan(resp)

	now := o.currentTime()
	for _, d := range decisions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		id := decisionID(d)
		entry := tau.Entry{
			ID:        "sdd-decision-" + id,
			Text:      d,
			Tags:      o.tags(),
			Source:    "sdd",
			Timestamp: now,
		}
		if err := o.safePut(ctx, entry); err != nil {
			// Per the spec: store failures are silently dropped
			// (non-aborting contract). The runtime logs the nil
			// return; the decision is lost this turn.
			continue
		}
	}
	return nil
}

// safeScan wraps scanDecisions with panic recovery. On panic it
// returns nil so the caller proceeds as if no decisions were found.
// The scanner is overridable via SetScanner for tests that need to
// exercise the recovery branch against a synthetic panicking scan.
func (o *DecisionsObserver) safeScan(resp *tau.Response) (out []string) {
	defer func() {
		_ = recover()
	}()
	o.mu.Lock()
	fn := o.scanner
	o.mu.Unlock()
	if fn == nil {
		out = scanDecisions(resp)
		return
	}
	out = fn(resp)
	return
}

// safePut wraps store.Put with panic recovery. Returns the underlying
// error on failure or nil if the call panicked.
func (o *DecisionsObserver) safePut(ctx context.Context, e tau.Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = nil
		}
	}()
	if o.store == nil {
		return nil
	}
	return o.store.Put(ctx, e)
}

// tags returns the tag slice for stored decisions. Always includes
// "sdd" and "decision"; the subsystem tag is appended when set.
func (o *DecisionsObserver) tags() []string {
	tags := []string{"sdd", "decision"}
	if o.tagSubsystem != "" {
		tags = append(tags, o.tagSubsystem)
	}
	return tags
}

// currentTime returns the observer's notion of "now" in UTC. Tests
// override via o.timeNow.
func (o *DecisionsObserver) currentTime() time.Time {
	o.mu.Lock()
	fn := o.timeNow
	o.mu.Unlock()
	if fn == nil {
		return time.Now().UTC()
	}
	return fn().UTC()
}

// SetTimeNow replaces the clock used for Timestamp. Test-only; not
// exported. Pass nil to revert to time.Now().UTC().
func (o *DecisionsObserver) SetTimeNow(fn func() time.Time) {
	o.mu.Lock()
	o.timeNow = fn
	o.mu.Unlock()
}

// SetScanner replaces the function used to extract decision markers
// from a Response. Test-only; not exported. Pass nil to revert to the
// package-level scanDecisions. Tests use this to inject a panicking
// scanner so the recovery branch in safeScan is exercised; production
// code never calls this setter.
func (o *DecisionsObserver) SetScanner(fn func(*tau.Response) []string) {
	o.mu.Lock()
	o.scanner = fn
	o.mu.Unlock()
}

// scanDecisions walks every TextContent block in resp.Content and
// extracts decision markers. Returns nil when none are found.
func scanDecisions(resp *tau.Response) []string {
	if resp == nil {
		return nil
	}
	var out []string
	for _, b := range resp.Content {
		tc, ok := b.(tau.TextContent)
		if !ok {
			continue
		}
		out = append(out, extractDecisionMarkers(tc.Text)...)
	}
	return out
}

// extractDecisionMarkers walks text line by line and returns each
// decision statement as a single trimmed string. Recognises three
// marker forms (design.md D7):
//
//   - `**Decision <id>**: <body>` — bolded inline form
//   - `D<n>. <body>` — dotted numbered form
//   - `Decision: <body>` — generic form
//
// Returns nil when text contains no decision markers.
func extractDecisionMarkers(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// **Decision <id>**: <body>
		if m := boldDecisionRE.FindStringSubmatch(trimmed); m != nil {
			out = append(out, strings.TrimSpace(m[1]+": "+m[2]))
			continue
		}

		// D<n>. <body>
		if m := dottedDecisionRE.FindStringSubmatch(trimmed); m != nil {
			out = append(out, strings.TrimSpace("D"+m[1]+". "+m[2]))
			continue
		}

		// Decision: <body>
		if m := genericDecisionRE.FindStringSubmatch(trimmed); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
			continue
		}
	}
	return out
}

var (
	// boldDecisionRE matches `**Decision <id>**: <body>` and captures
	// the id and body separately.
	boldDecisionRE = regexp.MustCompile(`^\*\*Decision\s+([^*]+?)\*\*:\s*(.+)$`)

	// dottedDecisionRE matches `D<n>. <body>` and captures n and body.
	dottedDecisionRE = regexp.MustCompile(`^D(\d+)\.\s+(.+)$`)

	// genericDecisionRE matches `Decision: <body>` and captures body.
	genericDecisionRE = regexp.MustCompile(`^Decision:\s+(.+)$`)
)

// decisionID returns a stable ID for a decision by hashing its text.
// Used as the Entry.ID so the same decision seen twice in successive
// turns overwrites rather than duplicates.
func decisionID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

// ensureDecisionsObserverSatisfiesResponseObserver is a compile-time
// assertion that *DecisionsObserver satisfies tau.ResponseObserver.
var _ tau.ResponseObserver = (*DecisionsObserver)(nil)
