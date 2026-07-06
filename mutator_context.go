// mutator_context.go — the ContextMutator RequestMutator.
//
// Implements design.md D6: a RequestMutator that prepends the active
// change's proposal.md, relevant spec deltas, and cited design.md decisions
// to req.System as tau.TextContent blocks. Active change is configured at
// construction (NewContextMutator); the mutator re-reads the files every
// call so changes between turns are picked up without restart.
//
// Per pkg/tau/middleware.go:45-47 a non-nil return aborts the turn —
// unacceptable for an augmentation mutator. The mutator returns nil on
// any file-read or parse error and leaves req unchanged (spec scenario
// "Context mutator is non-aborting on missing files"). Cancellation is
// the only condition that returns a non-nil error.
package sdd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	tau "github.com/taucentral/tau/pkg/tau"
)

// DefaultMaxContextBytes is the byte budget the ContextMutator caps
// injected content at when ContextOptions.MaxBytes is zero. Matches the
// 8 KiB default in design.md D6 / proposal.md.
const DefaultMaxContextBytes = 8192

// ContextOptions configures a ContextMutator. Zero-value behaviour:
// MaxBytes defaults to DefaultMaxContextBytes; the budget is shared
// across proposal, spec deltas, and decisions in that order.
type ContextOptions struct {
	// MaxBytes caps the total injected content. Zero means
	// DefaultMaxContextBytes. Excess content is truncated at a sentence
	// boundary when possible, else hard-cut, and a
	// `[truncated; see <path>]` marker is appended.
	MaxBytes int

	// IncludeSpecs selects whether the mutator injects spec deltas.
	// Default true (zero value treated as true for parity with the
	// headroom plugin's options).
	IncludeSpecs bool

	// IncludeDecisions selects whether the mutator injects design.md
	// decisions. Default true (same parity note).
	IncludeDecisions bool
}

// ContextMutator is a RequestMutator that prepends the active change's
// proposal.md, spec deltas, and cited design.md decisions to req.System.
//
// The mutator is safe for concurrent use: the byte-budget arithmetic is
// guarded by a mutex because the same mutator instance may be invoked
// from multiple goroutines in sequence (Settings.SteeringMode="all").
type ContextMutator struct {
	changeRoot string
	changeName string
	opts       ContextOptions

	mu sync.Mutex
}

// NewContextMutator returns a ContextMutator bound to the supplied
// changeRoot and changeName. changeRoot is the directory containing
// openspec/changes/<name>/; pass "" to resolve via
// defaultChangeRoot(). opts zero-value is treated as the defaults
// (MaxBytes = DefaultMaxContextBytes, IncludeSpecs = true,
// IncludeDecisions = true).
func NewContextMutator(changeRoot, changeName string, opts ContextOptions) *ContextMutator {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxContextBytes
	}
	// Treat zero-value IncludeSpecs / IncludeDecisions as "on". We
	// cannot distinguish "user explicitly set false" from "user left
	// zero" in Go without *bool, so callers who want to disable either
	// path MUST pass a non-zero MaxBytes (or extend the struct later).
	if opts.MaxBytes == DefaultMaxContextBytes {
		opts.IncludeSpecs = true
		opts.IncludeDecisions = true
	}
	return &ContextMutator{
		changeRoot: changeRoot,
		changeName: changeName,
		opts:       opts,
	}
}

// MutateRequest satisfies tau.RequestMutator. It reads the active
// change's proposal.md (and optionally specs/<capability>/spec.md
// deltas plus design.md decisions), truncates the combined content to
// the byte budget, and prepends it to req.System as a single
// tau.TextContent block.
//
// Returns nil on any file-read or parse failure (the mutator is
// non-aborting per the spec). Returns ctx.Err() when the context is
// cancelled.
func (m *ContextMutator) MutateRequest(ctx context.Context, req *tau.Request) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if req == nil {
		return nil
	}

	root := m.changeRoot
	if root == "" {
		abs, err := defaultChangeRoot()
		if err != nil {
			return nil
		}
		root = abs
	}
	changeDir := filepath.Join(root, m.changeName)
	if fi, err := os.Stat(changeDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return nil
	} else if !fi.IsDir() {
		return nil
	}

	proposalPath := filepath.Join(changeDir, "proposal.md")
	proposalBytes, err := os.ReadFile(proposalPath)
	if err != nil {
		// Malformed proposal.md / missing file: spec scenario
		// "Context mutator is non-aborting on missing files".
		return nil
	}

	// Lock around the byte-budget arithmetic; the same mutator instance
	// may be invoked concurrently when Settings.SteeringMode="all".
	m.mu.Lock()
	budget := m.opts.MaxBytes
	m.mu.Unlock()

	var combined strings.Builder
	combined.WriteString("# Active change: " + m.changeName + "\n\n")
	combined.WriteString("## proposal.md (from " + proposalPath + ")\n\n")
	writeTruncated(&combined, string(proposalBytes), budget, proposalPath)
	if combined.Len() >= budget {
		m.commit(req, combined.String())
		return nil
	}

	if m.opts.IncludeSpecs {
		specsBytes, _ := readChangeSpecs(changeDir)
		for capName, spec := range specsBytes {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if combined.Len() >= budget {
				break
			}
			remaining := budget - combined.Len()
			if remaining <= 0 {
				break
			}
			path := "specs/" + capName + "/spec.md"
			combined.WriteString("\n## spec delta: " + path + "\n\n")
			writeTruncated(&combined, string(spec), remaining, path)
		}
	}

	if m.opts.IncludeDecisions {
		designPath := filepath.Join(changeDir, "design.md")
		if designBytes, err := os.ReadFile(designPath); err == nil {
			if combined.Len() < budget {
				remaining := budget - combined.Len()
				decisions := extractDecisions(string(designBytes))
				if len(decisions) > 0 {
					combined.WriteString("\n## cited decisions\n\n")
					for _, d := range decisions {
						if combined.Len() >= budget {
							break
						}
						writeTruncated(&combined, d, remaining, "design.md")
						combined.WriteByte('\n')
					}
				}
			}
		}
	}

	m.commit(req, combined.String())
	return nil
}

// commit prepends a single TextContent block carrying body to req.System.
// Caller MUST hold no external locks; this method does not touch m.mu.
func (m *ContextMutator) commit(req *tau.Request, body string) {
	if req == nil || body == "" {
		return
	}
	block := tau.TextContent{Text: body}
	req.System = append([]tau.ContentBlock{block}, req.System...)
}

// writeTruncated appends body to b, truncating at a sentence boundary
// (preferring `. `, `! `, or `? `) when the full text would exceed the
// remaining budget. The truncation marker `[truncated; see <path>]` is
// appended after the truncated content. If body fits, no marker is
// added.
func writeTruncated(b *strings.Builder, body string, budget int, path string) {
	if budget <= 0 {
		return
	}
	if len(body) <= budget {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
		return
	}

	// Reserve room for the marker.
	marker := "\n[truncated; see " + path + "]\n"
	limit := budget - len(marker)
	if limit <= 0 {
		// Budget too small to fit anything useful; emit only the marker
		// so the model knows truncation happened.
		b.WriteString(strings.TrimSpace(marker))
		b.WriteByte('\n')
		return
	}

	// Find the last sentence boundary within the budget.
	head := body[:limit]
	cut := lastSentenceBoundary(head)
	if cut > 0 {
		b.WriteString(body[:cut])
	} else {
		b.WriteString(head)
	}
	b.WriteByte('\n')
	b.WriteString(strings.TrimPrefix(marker, "\n"))
}

// lastSentenceBoundary returns the byte offset of the last `. `, `! `,
// or `? ` in s, or 0 if none was found (caller hard-cuts).
func lastSentenceBoundary(s string) int {
	for _, sep := range []string{". ", "! ", "? "} {
		if idx := strings.LastIndex(s, sep); idx > 0 {
			return idx + len(sep)
		}
	}
	return 0
}

// extractDecisions walks design.md and returns one entry per decision
// marker (`### Decision <id>` / `**Decision <id>**:` / `D<n>.`).
// Returns nil when no decisions are present.
func extractDecisions(design string) []string {
	var out []string
	for _, line := range strings.Split(design, "\n") {
		trimmed := strings.TrimSpace(line)
		var id, body string
		switch {
		case strings.HasPrefix(trimmed, "### Decision "):
			rest := strings.TrimPrefix(trimmed, "### Decision ")
			// `### Decision <id>. <title>` — split at first ". "
			// so id captures only the numeric token.
			if idx := strings.Index(rest, ". "); idx > 0 {
				id = strings.TrimSpace(rest[:idx])
				body = strings.TrimSpace(rest[idx+2:])
			} else {
				id = strings.TrimSpace(rest)
				body = ""
			}
		case strings.HasPrefix(trimmed, "**Decision ") && strings.Contains(trimmed, "**:"):
			rest := strings.TrimPrefix(trimmed, "**Decision ")
			if idx := strings.Index(rest, "**:"); idx >= 0 {
				id = strings.TrimSpace(rest[:idx])
				body = strings.TrimSpace(rest[idx+len("**:"):])
			}
		default:
			m := decisionDottedRE.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			id = strings.TrimSpace(m[1])
			body = strings.TrimSpace(m[2])
		}
		if id == "" {
			continue
		}
		entry := "D" + id + ": " + body
		if body == "" {
			entry = "D" + id
		}
		out = append(out, entry)
	}
	return out
}

// decisionDottedRE matches `D1.` / `D2.` style markers in design.md.
// Decision id is captured in group 1; the body (rest of line) in group 2.
var decisionDottedRE = regexp.MustCompile(`^D(\d+)\.\s*(.*)$`)

// ensureContextMutatorSatisfiesRequestMutator is a compile-time
// assertion that *ContextMutator satisfies tau.RequestMutator.
var _ tau.RequestMutator = (*ContextMutator)(nil)
