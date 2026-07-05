# tau-plugins/sdd

A tau plugin that realises the spec-driven development (SDD) workflow
patterns documented in `docs/input/context/plugins/openspec.md`
natively in Go. The plugin extends tau by providing implementations of
tau's extension interfaces (`RequestMutator`, `ResponseObserver`,
`HeadlessTool`, `Command`, `Orchestrator`, all declared in `pkg/tau`).
No Python sidecar, no Node runtime, no CGO.

The plugin never calls `tau.CreateAgentSession`, `agent.Run`,
`tau.RegisterProvider`, or any orchestration API directly. The embedder
constructs the session and wires the plugin's returned middleware,
tools, commands, and orchestrator into a `tau.Options` value the
embedder owns.

The reference basis for this plugin's design is
`docs/input/context/plugins/openspec.md`. The OpenSpec change proposal
lives at `openspec/changes/add-sdd-plugin/`.

## Status

**v0.x — scaffold complete, source implemented.** The implementation
mirrors the task list at `openspec/changes/add-sdd-plugin/tasks.md`.

## What it does

The plugin ships five components, all composed from existing tau SDK
seams (`pkg/tau/`):

1. **Four slash commands** (`/propose`, `/explore`, `/apply`,
   `/archive`) wrapping the SDD lifecycle.
2. **`validate_change` `HeadlessTool`** that runs the OpenSpec rule
   set (ported verbatim to Go) against `openspec/changes/<name>/`.
3. **`ContextMutator` `RequestMutator`** that auto-injects the active
   change's `proposal.md` + relevant `specs/<subsystem>/` deltas +
   cited `design.md` decisions into the system prompt.
4. **`DecisionsObserver` `ResponseObserver`** that extracts decision
   blocks from assistant responses and writes them to a `Store`
   keyed by `(subsystem, requirement)`.
5. **`SDDOrchestrator`** — a thin wrapper over the existing
   `tau.NewSequentialOrchestrator` that parses `tasks.md` into
   `PhaseSpec` values (one per section) with `DependsOn` edges.

## Entry points

The plugin exposes four constructor functions in `register.go`. Each
returns fresh values per call so independent embedder configurations
never share state:

| Function | Returns | Maps to |
|----------|---------|---------|
| `Middleware(opts RegisterOptions, store tau.Store) []any` | RequestMutator + ResponseObserver instances | `tau.Options.Middleware` |
| `Tools(store tau.Store) []tau.HeadlessTool` | The `validate_change` tool | `tau.Options.Tools` (typically appended to `tau.BuiltinTools()`) |
| `Commands(opts RegisterOptions, store tau.Store) []tau.Command` | The four SDD slash commands | `tau.Options.SlashCommands` (after wrapping in a `*tau.Registry`) |
| `Orchestrator(parent *tau.AgentSession, opts RegisterOptions) (tau.Orchestrator, error)` | The SDD phase orchestrator | `tau.Options.Orchestrator` |

## Embedding

```go
package main

import (
	"context"
	"log"

	tau "github.com/coevin/tau/pkg/tau"
	sdd "github.com/taucentral/sdd"
)

func main() {
	ctx := context.Background()

	store, err := tau.NewFileStore(".sdd-cache")
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer store.Close()

	opts := sdd.RegisterOptions{
		ContextMutator:    true,
		DecisionsObserver: true,
		ActiveChange:      "my-change",
		MaxContextBytes:   8192,
	}

	mw := sdd.Middleware(opts, store)
	tools := append(tau.BuiltinTools(), sdd.Tools(store)...)

	orch, err := sdd.Orchestrator(nil, opts)
	if err != nil {
		log.Fatalf("orchestrator: %v", err)
	}

	registry := tau.NewRegistry()
	for _, cmd := range sdd.Commands(opts, store) {
		registry.Register(cmd)
	}

	sess, err := tau.CreateAgentSession(ctx, tau.Options{
		Cwd:            ".",
		Model:          "claude-opus-4-5-20251101",
		LLMClient:      tau.NewFauxProvider("hello from the model"),
		Tools:          tools,
		Settings:       tau.DefaultSettings(),
		StateManager:   tau.NewInMemoryManager("."),
		ContextWindow:  200000,
		Middleware:     mw,
		Store:          store,
		SlashCommands:  registry,
		Orchestrator:   orch,
	})
	if err != nil {
		log.Fatalf("create session: %v", err)
	}
	defer sess.Shutdown(ctx)

	if err := sess.Run(ctx, "Work on the change."); err != nil {
		log.Fatalf("run: %v", err)
	}
}
```

The snippet above is complete. Drop it into `main.go` in a module that
`require`s both `github.com/coevin/tau` and
`github.com/taucentral/sdd` and it will compile standalone.

Running the resulting binary requires `openspec/changes/my-change/tasks.md`
to exist on disk (the `sdd.Orchestrator` constructor parses it at startup
and returns `ErrTasksMissing` otherwise). Scaffold it first by invoking
the `/propose my-change` command, or copy the four-artifact layout from
any archived change under `openspec/changes/archive/`.

Replace `tau.NewFauxProvider` with a real LLM client for production
use. See `pkg/tau/doc.go` for the canonical embedding pattern.

## License

To be determined. The plugin is currently unlicensed pending a
decision on whether to match tau's license or ship under a separate
one.
