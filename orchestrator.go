// orchestrator.go — the SDDOrchestrator wrapper.
//
// Implements design.md D8: a thin constructor over
// tau.NewSequentialOrchestrator that parses
// openspec/changes/<changeName>/tasks.md into a []tau.PhaseSpec — one
// phase per `## N.` section header — and returns a tau.Orchestrator
// driven by those phases. Each phase's DependsOn references the
// immediately preceding section (the first section has no dependencies).
//
// The wrapper does not implement Orchestrator itself; the returned value
// is whatever tau.NewSequentialOrchestrator returns. This keeps the
// dependency-graph logic in one place (tau's orchestrator) and the
// SDD-specific parsing in another (the plugin).
package sdd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	tau "github.com/coevin/tau/pkg/tau"
)

// ErrTasksMissing is returned by NewSDDOrchestrator when the change
// directory or its tasks.md is missing. Detectable via errors.Is so
// callers can distinguish "missing change" from a parse failure.
var ErrTasksMissing = errors.New("sdd: tasks.md missing")

// ErrTasksUnparseable is returned when tasks.md exists but could not
// be parsed (no `## ` section headers found, or the file failed to
// read). Detectable via errors.Is.
var ErrTasksUnparseable = errors.New("sdd: tasks.md unparseable")

// sectionNumberRE captures the leading number of a `## N. <title>`
// section header. Used to order phases by source position. Both
// "1." and "1)" numbering styles are accepted.
var sectionNumberRE = regexp.MustCompile(`^(\d+)[.)]?\s+(.+)$`)

// NewSDDOrchestrator parses
// openspec/changes/<changeName>/tasks.md into a []tau.PhaseSpec and
// returns a tau.Orchestrator constructed via
// tau.NewSequentialOrchestrator driven by those phases.
//
// Each phase corresponds to one `## ` section header. The phase's Name
// is the section title (e.g. "1. Module scaffold"); the phase's Prompt
// carries the section body so the child session has the task list
// inline. DependsOn references the immediately preceding section's
// number (the first section has no dependencies).
//
// Returns:
//
//   - (nil, ErrTasksMissing) when changeDir or tasks.md is absent.
//   - (nil, ErrTasksUnparseable) when tasks.md exists but has no
//     parseable `## ` section headers.
//   - (orchestrator, nil) on success.
//
// parent is passed through to tau.NewSequentialOrchestrator. Pass nil
// for headless construction; the embedder wires the parent when
// constructing the real session.
func NewSDDOrchestrator(parent *tau.AgentSession, changeName string) (tau.Orchestrator, error) {
	return NewSDDOrchestratorFromRoot(parent, "", changeName)
}

// NewSDDOrchestratorFromRoot is like NewSDDOrchestrator but accepts an
// explicit changeRoot. Pass "" for changeRoot to resolve via
// defaultChangeRoot() ("openspec/changes" relative to os.Getwd()).
// Exposed so tests can target a temp directory.
func NewSDDOrchestratorFromRoot(parent *tau.AgentSession, changeRoot, changeName string) (tau.Orchestrator, error) {
	root := changeRoot
	if root == "" {
		abs, err := defaultChangeRoot()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTasksMissing, err)
		}
		root = abs
	}
	changeDir := filepath.Join(root, changeName)
	if fi, err := os.Stat(changeDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrTasksMissing, changeDir)
		}
		return nil, fmt.Errorf("%w: %v", ErrTasksMissing, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%w: not a directory: %s", ErrTasksMissing, changeDir)
	}

	tasksPath := filepath.Join(changeDir, "tasks.md")
	tasksBytes, err := os.ReadFile(tasksPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrTasksMissing, tasksPath)
		}
		return nil, fmt.Errorf("%w: %v", ErrTasksUnparseable, err)
	}

	phases, err := parsePhases(tasksBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTasksUnparseable, err)
	}
	if len(phases) == 0 {
		return nil, fmt.Errorf("%w: no `## ` section headers found in %s", ErrTasksUnparseable, tasksPath)
	}

	orch := tau.NewSequentialOrchestrator(parent)
	// The sequential orchestrator's spec is built lazily inside Run;
	// callers pass the spec via the OrchestrationSpec.Phases field at
	// Run time. We attach the parsed phases to the orchestrator by
	// wrapping it in a thin *phasesCarrier so NewSDDOrchestratorFromRoot
	// can hand the caller a ready-to-Run orchestrator that already
	// knows its phases.
	return &sddOrchestrator{
		inner: orch,
		spec: tau.OrchestrationSpec{
			Phases:      phases,
			MergePolicy: tau.MergePolicyAppend,
		},
	}, nil
}

// parsePhases walks tasks.md and returns one PhaseSpec per `## ` section
// header in source order. Each phase's DependsOn references the
// immediately preceding section's Name (the first section has empty
// DependsOn). Returns (nil, error) when the markdown has zero `## `
// headers.
func parsePhases(tasks []byte) ([]tau.PhaseSpec, error) {
	names, bodies, err := ParseSectionsOrdered(tasks)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}

	// PhaseSpec.Name is the section title verbatim ("## 1. Module
	// scaffold" -> "1. Module scaffold"). DependsOn references the
	// prior phase by Name (NOT by section number) so the dependency
	// edge survives renumbering.
	phases := make([]tau.PhaseSpec, 0, len(names))
	for i, name := range names {
		body := bodies[name]
		prompt := buildPhasePrompt(name, body)
		phase := tau.PhaseSpec{
			Name:    name,
			Prompt:  prompt,
			Options: nil,
		}
		if i > 0 {
			phase.DependsOn = []string{names[i-1]}
		}
		phases = append(phases, phase)
	}
	return phases, nil
}

// buildPhasePrompt assembles the user-message text handed to a child
// session when running one phase. The prompt carries the section title
// and the verbatim task list so the model has the work inline.
func buildPhasePrompt(sectionName, sectionBody string) string {
	var b strings.Builder
	b.WriteString("Work on SDD section: ")
	b.WriteString(sectionName)
	b.WriteString("\n\n")
	b.WriteString(sectionBody)
	return b.String()
}

// sddOrchestrator wraps a tau.Orchestrator and remembers the
// OrchestrationSpec derived from tasks.md so the embedder can call Run
// without re-specifying the phases. It satisfies tau.Orchestrator by
// delegation.
//
// The mu mutex guards spec because the tau.Orchestrator contract permits
// Run and Err to be called concurrently (callers drain Run's channel in
// one goroutine and poll Err in another). Without the lock, the
// embedder-override write in Run would race with Phases/Err reads.
type sddOrchestrator struct {
	inner tau.Orchestrator

	mu   sync.RWMutex
	spec tau.OrchestrationSpec
}

// Run hands the assembled spec to the inner orchestrator. When the caller
// passes a spec with at least one phase, that spec overrides the spec
// computed at construction time (embedder override). Otherwise the
// construction-time spec is used as-is.
func (o *sddOrchestrator) Run(ctx context.Context, spec tau.OrchestrationSpec) (<-chan tau.SessionEvent, error) {
	if o == nil {
		return nil, errors.New("sdd: orchestrator is nil")
	}
	o.mu.Lock()
	if len(spec.Phases) > 0 {
		o.spec = spec
	}
	inner := o.inner
	runSpec := o.spec
	o.mu.Unlock()
	if inner == nil {
		return nil, errors.New("sdd: orchestrator has no inner sequential orchestrator")
	}
	return inner.Run(ctx, runSpec)
}

// Err delegates to the inner orchestrator.
func (o *sddOrchestrator) Err() error {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	inner := o.inner
	o.mu.RUnlock()
	if inner == nil {
		return nil
	}
	return inner.Err()
}

// Phases returns the PhaseSpec slice derived from tasks.md. Exposed
// (via type assertion in tests) so the orchestrator test can assert
// the dependency edges without running a real session.
func (o *sddOrchestrator) Phases() []tau.PhaseSpec {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.spec.Phases
}

// ensureSDDOrchestratorSatisfiesOrchestrator is a compile-time
// assertion that *sddOrchestrator satisfies tau.Orchestrator.
var _ tau.Orchestrator = (*sddOrchestrator)(nil)
