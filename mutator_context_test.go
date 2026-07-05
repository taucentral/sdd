// mutator_context_test.go — tests for the ContextMutator RequestMutator.
//
// Covers the three spec scenarios from the change's spec delta:
//   - "Context mutator is non-aborting on missing files"
//   - "Context mutator respects byte budget"
//   - "Context mutator injects proposal, spec deltas, and design decisions"
package sdd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tau "github.com/coevin/tau/pkg/tau"
)

func TestContextMutator_NonAbortingOnMissingChangeDir(t *testing.T) {
	root := t.TempDir()
	mut := NewContextMutator(root, "does-not-exist", ContextOptions{MaxBytes: 4096})
	req := &tau.Request{}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Errorf("MutateRequest: want nil for missing change dir, got %v", err)
	}
	if len(req.System) != 0 {
		t.Errorf("System was mutated despite missing change dir: %v", req.System)
	}
}

func TestContextMutator_NonAbortingOnMissingProposal(t *testing.T) {
	root := t.TempDir()
	// Change dir exists but proposal.md does not.
	if err := os.MkdirAll(filepath.Join(root, "broken"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mut := NewContextMutator(root, "broken", ContextOptions{MaxBytes: 4096})
	req := &tau.Request{}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Errorf("MutateRequest: want nil for missing proposal, got %v", err)
	}
	if len(req.System) != 0 {
		t.Errorf("System mutated despite missing proposal: %v", req.System)
	}
}

func TestContextMutator_InjectsProposal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok", "proposal.md"),
		"## Why\nsome long-form reason text here\n## What Changes\n- thing\n")
	mut := NewContextMutator(root, "ok", ContextOptions{MaxBytes: 4096})
	req := &tau.Request{}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	if len(req.System) != 1 {
		t.Fatalf("System len = %d, want 1", len(req.System))
	}
	tc, ok := req.System[0].(tau.TextContent)
	if !ok {
		t.Fatalf("System[0] type = %T, want TextContent", req.System[0])
	}
	for _, want := range []string{"Active change: ok", "proposal.md", "## Why"} {
		if !strings.Contains(tc.Text, want) {
			t.Errorf("missing %q in injected block; got:\n%s", want, tc.Text)
		}
	}
}

func TestContextMutator_InjectsSpecDeltas(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cap", "proposal.md"), "## Why\nreason text\n")
	writeFile(t, filepath.Join(root, "cap", "specs", "alpha", "spec.md"),
		"## ADDED Requirements\n### Requirement: A\nThe system SHALL a.\n")
	mut := NewContextMutator(root, "cap", ContextOptions{MaxBytes: 8192})
	req := &tau.Request{}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	tc := req.System[0].(tau.TextContent)
	if !strings.Contains(tc.Text, "spec delta:") || !strings.Contains(tc.Text, "alpha/spec.md") {
		t.Errorf("spec delta not injected; got:\n%s", tc.Text)
	}
	if !strings.Contains(tc.Text, "ADDED Requirements") {
		t.Errorf("spec delta content missing; got:\n%s", tc.Text)
	}
}

func TestContextMutator_InjectsDesignDecisions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "d", "proposal.md"), "## Why\nreason text\n")
	writeFile(t, filepath.Join(root, "d", "design.md"), `## Decisions

### Decision 1. Use adapters
We chose adapter types.

D2. Use three interfaces.
Splitting keeps each interface single-purpose.
`)
	mut := NewContextMutator(root, "d", ContextOptions{MaxBytes: 8192})
	req := &tau.Request{}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	tc := req.System[0].(tau.TextContent)
	if !strings.Contains(tc.Text, "cited decisions") {
		t.Errorf("missing cited decisions header; got:\n%s", tc.Text)
	}
	if !strings.Contains(tc.Text, "D1:") {
		t.Errorf("missing D1 marker; got:\n%s", tc.Text)
	}
	if !strings.Contains(tc.Text, "D2:") {
		t.Errorf("missing D2 marker; got:\n%s", tc.Text)
	}
}

func TestContextMutator_TruncatesAtBudget(t *testing.T) {
	root := t.TempDir()
	huge := "## Why\n" + strings.Repeat("alpha ", 1000) + "\n"
	writeFile(t, filepath.Join(root, "big", "proposal.md"), huge)
	mut := NewContextMutator(root, "big", ContextOptions{MaxBytes: 128})
	req := &tau.Request{}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	tc := req.System[0].(tau.TextContent)
	if !strings.Contains(tc.Text, "[truncated;") {
		t.Errorf("expected truncation marker; got (len=%d):\n%s", len(tc.Text), tc.Text)
	}
	// Sanity: total injected payload must not blow wildly past the budget.
	if len(tc.Text) > 512 {
		t.Errorf("injected payload len = %d, expected near 128", len(tc.Text))
	}
}

func TestContextMutator_PreservesExistingSystemBlocks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok", "proposal.md"), "## Why\nreason\n")
	mut := NewContextMutator(root, "ok", ContextOptions{MaxBytes: 4096})
	prior := tau.TextContent{Text: "existing system prompt"}
	req := &tau.Request{System: []tau.ContentBlock{prior}}
	if err := mut.MutateRequest(context.Background(), req); err != nil {
		t.Fatalf("MutateRequest: %v", err)
	}
	if len(req.System) != 2 {
		t.Fatalf("System len = %d, want 2", len(req.System))
	}
	if req.System[1] != prior {
		t.Errorf("prior block not preserved at index 1: got %v", req.System[1])
	}
}

func TestContextMutator_ConcurrentSafe(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok", "proposal.md"), "## Why\nreason\n")
	mut := NewContextMutator(root, "ok", ContextOptions{MaxBytes: 4096})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &tau.Request{}
			_ = mut.MutateRequest(context.Background(), req)
			if len(req.System) != 1 {
				t.Errorf("System len = %d, want 1", len(req.System))
			}
		}()
	}
	wg.Wait()
}

func TestContextMutator_NilRequestIsSafe(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok", "proposal.md"), "## Why\nreason\n")
	mut := NewContextMutator(root, "ok", ContextOptions{MaxBytes: 4096})
	if err := mut.MutateRequest(context.Background(), nil); err != nil {
		t.Errorf("MutateRequest(nil): want nil, got %v", err)
	}
}

func TestExtractDecisions_AllThreeFormats(t *testing.T) {
	design := `## Decisions

### Decision 1. First
body

**Decision 2**: Second body

D3. Third body
`
	got := extractDecisions(design)
	if len(got) != 3 {
		t.Fatalf("extractDecisions len = %d, want 3 (%v)", len(got), got)
	}
	for _, want := range []string{"D1:", "D2:", "D3:"} {
		found := false
		for _, g := range got {
			if strings.HasPrefix(g, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %s prefix in %v", want, got)
		}
	}
}

func TestLastSentenceBoundary_PrefersPeriodSpace(t *testing.T) {
	cases := map[string]int{
		"no boundary here":  0,
		"end. more text":    len("end. "),
		"end! more":         len("end! "),
		"end? more":         len("end? "),
		"multi. sent. end.": len("multi. sent. "), // last ". " wins; trailing "." has no space
	}
	for in, want := range cases {
		got := lastSentenceBoundary(in)
		if got != want {
			t.Errorf("lastSentenceBoundary(%q) = %d, want %d", in, got, want)
		}
	}
}
