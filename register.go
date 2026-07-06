// register.go — the four embedder entry points.
//
// Mirrors the headroom plugin's entry-point shape (D11): four
// constructor functions — Middleware, Tools, Commands, Orchestrator —
// each returning fresh values per call. Each accepts a RegisterOptions
// struct (and a tau.Store where relevant) so the embedder can select
// components declaratively.
//
// The plugin never calls tau.CreateAgentSession, agent.Run, or any
// orchestration API; the embedder wires the returned values into
// tau.Options.
package sdd

import (
	"errors"

	tau "github.com/taucentral/tau/pkg/tau"
)

// RegisterOptions selects which plugin components the embedder wants
// wired into tau.Options, plus the configuration each component needs.
// Every component is opt-in; a zero-value RegisterOptions produces an
// empty Middleware slice.
type RegisterOptions struct {
	// ContextMutator enables the SDD context-injection RequestMutator
	// (proposal.md + spec deltas + design.md decisions prepended to
	// req.System). Requires ActiveChange to be set.
	ContextMutator bool

	// DecisionsObserver enables the SDD decisions ResponseObserver
	// that mines assistant responses for `**Decision <id>**:` /
	// `D<n>.` markers and writes them to the Store.
	DecisionsObserver bool

	// MaxContextBytes configures the ContextMutator's byte budget.
	// Zero defaults to DefaultMaxContextBytes (8 KiB). Ignored when
	// ContextMutator is false.
	MaxContextBytes int

	// ActiveChange names the change directory under openspec/changes/
	// the ContextMutator reads and the Orchestrator entry point
	// parses tasks.md from. Empty is valid for Middleware and Tools
	// (they do not need it) but causes Commands and Orchestrator to
	// produce no-op / error results.
	ActiveChange string

	// ChangeRoot is the absolute path to openspec/changes/. Empty
	// means "resolve via defaultChangeRoot()" at use time.
	ChangeRoot string

	// TagSubsystem is the third tag (after "sdd" and "decision")
	// attached to entries the DecisionsObserver writes. Empty omits
	// the subsystem tag. Recommended value: the capability name
	// (e.g. "spec-driven-development").
	TagSubsystem string
}

// Middleware returns the requested middleware components as a slice of
// `any`, each element of which satisfies at least one of tau's
// middleware interfaces (RequestMutator or ResponseObserver). The
// embedder passes the returned slice directly into tau.Options.Middleware.
//
// Each call returns fresh, independent values; calling Middleware twice
// with the same opts yields two independent sets of middleware.
//
// store is the embedder-supplied tau.Store the DecisionsObserver uses
// to persist extracted decisions. Pass nil to disable persistence (the
// observer becomes a no-op); for real use, supply tau.NewFileStore or
// an equivalent implementation.
func Middleware(opts RegisterOptions, store tau.Store) []any {
	var out []any
	if opts.ContextMutator {
		out = append(out, NewContextMutator(opts.ChangeRoot, opts.ActiveChange, ContextOptions{
			MaxBytes:         opts.MaxContextBytes,
			IncludeSpecs:     true,
			IncludeDecisions: true,
		}))
	}
	if opts.DecisionsObserver {
		out = append(out, NewDecisionsObserver(store, opts.TagSubsystem))
	}
	return out
}

// Tools returns the plugin's headless tools. The slice always contains
// exactly one element: the `validate_change` tool. The embedder passes
// the returned slice (typically appended to tau.BuiltinTools()) into
// tau.Options.Tools.
//
// store is accepted for symmetry with the headroom plugin's Tools()
// signature; the validate_change tool does not currently use the store.
// Pass nil when no store is configured.
func Tools(store tau.Store) []tau.HeadlessTool {
	_ = store // reserved for future validate variants; current tool is stateless
	return []tau.HeadlessTool{NewValidateTool("")}
}

// Commands returns the four canonical SDD slash commands. Each call
// returns fresh command values; two calls produce no shared pointers
// across corresponding elements.
//
// opts.ActiveChange and opts.ChangeRoot flow into the commands that
// need them (/apply, /archive). The /propose and /explore commands
// ignore them.
//
// store is accepted for symmetry with the headroom plugin's
// register.go; no SDD command currently uses the store.
func Commands(opts RegisterOptions, store tau.Store) []tau.Command {
	_ = store
	return []tau.Command{
		NewProposeCommand(opts.ChangeRoot),
		NewExploreCommand(),
		NewApplyCommand(opts.ChangeRoot, opts.ActiveChange),
		NewArchiveCommand(opts.ChangeRoot),
	}
}

// Orchestrator returns a tau.Orchestrator that drives the active
// change's tasks.md as a sequence of phases. Delegates to
// NewSDDOrchestrator. Returns (nil, err) when opts.ActiveChange is
// empty or the change directory is missing.
//
// parent is the agent session the orchestrator will spawn child
// sessions from. Pass nil for headless construction (the embedder
// wires the parent when constructing the real session).
func Orchestrator(parent *tau.AgentSession, opts RegisterOptions) (tau.Orchestrator, error) {
	if opts.ActiveChange == "" {
		return nil, errors.New("sdd: ActiveChange is required for Orchestrator")
	}
	return NewSDDOrchestratorFromRoot(parent, opts.ChangeRoot, opts.ActiveChange)
}
