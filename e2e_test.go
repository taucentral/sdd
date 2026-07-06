// e2e_test.go — end-to-end integration tests for the SDD plugin.
//
// Two layers of coverage live in this file:
//
//   - Task 10.2: TestE2E_RealSession_WiresAllEntryPoints constructs a
//     real *tau.AgentSession via tau.CreateAgentSession with Middleware
//     + Tools + Commands + Orchestrator wired, runs one turn via
//     tau.NewFauxProvider, and asserts the four spec-mandated
//     conditions (a)-(d).
//   - Task 8.3: TestE2E_SDDOrchestrator_RunNoPanic builds an
//     SDDOrchestrator against a real parent session and asserts that
//     Run produces the expected phase count, dependency edges, and
//     completes without panic under -race.
//
// The remaining TestE2E_* tests exercise the four lifecycle functions
// (Propose / Apply / Archive) directly. They predate the SDK contract
// test and provide additional coverage of the filesystem layer; they
// are NOT a substitute for the real-session test above.
package sdd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tau "github.com/taucentral/tau/pkg/tau"
)

// newChangeRoot returns a temp directory structured like
// <tmp>/openspec/changes/ so Archive's canonicalSpecsDir derivation
// (filepath.Join(filepath.Dir(changeRoot), "specs")) lands inside the
// tempdir rather than in /tmp.
func newChangeRoot(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "openspec", "changes")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatalf("mkdir changeRoot: %v", err)
	}
	return root
}

// canonicalRoot returns the openspec/ directory that holds both
// changes/ and specs/. Used to verify the fold-and-move path.
func canonicalRoot(changeRoot string) string {
	return filepath.Dir(changeRoot)
}

// TestE2E_ProposeValidateApplyArchive walks the four lifecycle stages
// end-to-end against a single temp change root. It verifies that:
//
//   - Propose creates the four canonical artifacts.
//   - The validate_change tool, run against the freshly-scaffolded
//     change, surfaces the stub proposal.md's TODO Why body.
//   - Apply parses the stub tasks.md and reports per-section progress.
//   - Archive refuses while tasks are unchecked.
//   - After all tasks are checked off, Archive moves the change to
//     archive/<date>-<name>/ and (when spec deltas exist) folds them
//     into the canonical openspec/specs/<cap>/spec.md.
func TestE2E_ProposeValidateApplyArchive(t *testing.T) {
	root := newChangeRoot(t)
	ctx := context.Background()

	// Stage 1: /propose scaffolds the change directory.
	proposeOut, err := Propose(ctx, root, "add-e2e-cap")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if !strings.Contains(proposeOut, "add-e2e-cap") {
		t.Errorf("Propose output missing change name: %q", proposeOut)
	}
	tasksPath := filepath.Join(root, "add-e2e-cap", "tasks.md")
	if _, err := os.Stat(tasksPath); err != nil {
		t.Fatalf("tasks.md not scaffolded: %v", err)
	}

	// Stage 2: validate_change against the freshly-scaffolded state.
	// The stub proposal.md has "TODO: explain why..." (under the min
	// length), so the report SHOULD surface at least one finding.
	tool := NewValidateTool(root)
	args := []byte(`{"change":"add-e2e-cap"}`)
	res, err := tool.Execute(ctx, tau.ToolCall{Args: args})
	if err != nil {
		t.Fatalf("validate Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("validate IsError=true on scaffolded change: %v", res.Content)
	}
	tc, ok := res.Content[0].(tau.TextContent)
	if !ok {
		t.Fatalf("Content[0] = %T, want TextContent", res.Content[0])
	}
	if !strings.Contains(tc.Text, "WARNINGS:") && !strings.Contains(tc.Text, "ERRORS:") {
		t.Errorf("expected findings against stub; got:\n%s", tc.Text)
	}

	// Stage 3: /apply walks the stub tasks.md and reports progress.
	applyOut, err := Apply(ctx, nil, root, "add-e2e-cap")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(applyOut, "Section 1") || !strings.Contains(applyOut, "0/") {
		t.Errorf("Apply output missing Section 1 progress: %q", applyOut)
	}

	// Stage 4a: archive refuses while tasks are still unchecked.
	if _, err := Archive(ctx, root, "add-e2e-cap"); err == nil {
		t.Fatalf("Archive: expected ErrUncheckedTasks, got nil")
	}

	// Stage 4b: check off every task and re-archive. The fold-and-move
	// path only fires when tasks are done.
	checkAllTasks(t, tasksPath)
	archiveOut, err := Archive(ctx, root, "add-e2e-cap")
	if err != nil {
		t.Fatalf("Archive after checkAllTasks: %v", err)
	}
	if !strings.Contains(archiveOut, "archived") {
		t.Errorf("Archive output missing 'archived': %q", archiveOut)
	}
	// Source dir gone.
	if _, err := os.Stat(filepath.Join(root, "add-e2e-cap")); !os.IsNotExist(err) {
		t.Errorf("change dir still exists after Archive; stat err = %v", err)
	}
	// Archive dir contains a dated subdir.
	entries, err := os.ReadDir(filepath.Join(root, "archive"))
	if err != nil {
		t.Fatalf("ReadDir archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive has %d entries, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), "-add-e2e-cap") {
		t.Errorf("archive entry = %q, want suffix -add-e2e-cap", entries[0].Name())
	}
}

// TestE2E_FoldSpecIntoCanonicalOnArchive exercises the spec-fold path.
// When the change's specs/<cap>/spec.md is the only spec for that
// capability, Archive creates openspec/specs/<cap>/spec.md from it.
func TestE2E_FoldSpecIntoCanonicalOnArchive(t *testing.T) {
	root := newChangeRoot(t)
	ctx := context.Background()

	if _, err := Propose(ctx, root, "fold-cap"); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	// Write an ADDED delta and check off all tasks.
	delta := "## ADDED Requirements\n" +
		"### Requirement: X\nThe system SHALL x.\n" +
		"#### Scenario: ok\n- WHEN x\n"
	writeFile(t, filepath.Join(root, "fold-cap", "specs", "cap-x", "spec.md"), delta)
	checkAllTasks(t, filepath.Join(root, "fold-cap", "tasks.md"))

	if _, err := Archive(ctx, root, "fold-cap"); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// Canonical spec should now exist with the ADDED requirement.
	canonical := filepath.Join(canonicalRoot(root), "specs", "cap-x", "spec.md")
	body, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("ReadFile canonical %s: %v", canonical, err)
	}
	if !strings.Contains(string(body), "ADDED Requirements") {
		t.Errorf("canonical spec missing ADDED block; got:\n%s", body)
	}
	if !strings.Contains(string(body), "Requirement: X") {
		t.Errorf("canonical spec missing Requirement: X; got:\n%s", body)
	}
}

// TestE2E_RejectSecondActiveChange verifies the "only one active
// change at a time" rule: Propose must refuse while a sibling is active.
func TestE2E_RejectSecondActiveChange(t *testing.T) {
	root := newChangeRoot(t)
	ctx := context.Background()

	if _, err := Propose(ctx, root, "first"); err != nil {
		t.Fatalf("Propose first: %v", err)
	}
	if _, err := Propose(ctx, root, "second"); err == nil {
		t.Errorf("Propose second: expected ErrAnotherChangeActive, got nil")
	}
}

// TestE2E_FullCycleWithMultipleSections walks propose + apply over a
// multi-section tasks.md (rewritten after propose) to confirm the
// per-section progress reporting is end-to-end correct.
func TestE2E_FullCycleWithMultipleSections(t *testing.T) {
	root := newChangeRoot(t)
	ctx := context.Background()

	if _, err := Propose(ctx, root, "multi"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// Replace the stub tasks.md with a 3-section body.
	writeFile(t, filepath.Join(root, "multi", "tasks.md"),
		"## 1. A\n- [x] 1.1 done\n- [ ] 1.2 todo\n"+
			"## 2. B\n- [x] 2.1 done\n- [x] 2.2 done\n"+
			"## 3. C\n- [ ] 3.1 todo\n")
	out, err := Apply(ctx, nil, root, "multi")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, want := range []string{
		"Section 1 (1. A): 1/2",
		"Section 2 (2. B): 2/2",
		"Section 3 (3. C): 0/1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Apply output missing %q; got:\n%s", want, out)
		}
	}
}

// checkAllTasks rewrites tasks.md so every `- [ ]` becomes `- [x]`.
// This lets the e2e test complete the archive stage without manually
// enumerating each task line.
func checkAllTasks(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tasks.md: %v", err)
	}
	converted := strings.ReplaceAll(string(body), "- [ ]", "- [x]")
	if err := os.WriteFile(path, []byte(converted), 0600); err != nil {
		t.Fatalf("write tasks.md: %v", err)
	}
}

// captureObserver is a sibling ResponseObserver that records the
// post-mutation req.System it observes during a turn. The SDD
// ContextMutator runs as a RequestMutator BEFORE the LLM call;
// ResponseObservers fire AFTER, on the same turn. By the time
// ObserveResponse is invoked, req.System already reflects every
// RequestMutator's injection — so a sibling observer that records
// req.System sees exactly what the model saw.
//
// Used by TestE2E_RealSession_WiresAllEntryPoints to assert task
// 10.2 condition (d): the ContextMutator injected the active
// change's proposal text into the system prompt.
type captureObserver struct {
	mu         sync.Mutex
	lastSystem []tau.ContentBlock
}

func (c *captureObserver) ObserveResponse(ctx context.Context, req *tau.Request, resp *tau.Response, streamErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if req == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Copy the slice so a later mutation by the runtime cannot affect
	// the recorded value.
	c.lastSystem = append([]tau.ContentBlock(nil), req.System...)
	return nil
}

// noOpOrchestrator is a placeholder tau.Orchestrator used to satisfy
// Options.Orchestrator != nil on a parent session so Spawn does not
// return ErrNoOrchestrator. The runtime NEVER calls any method on
// Options.Orchestrator; the test exercises the SDD orchestrator
// directly via its own Run.
type noOpOrchestrator struct{}

func (noOpOrchestrator) Run(ctx context.Context, spec tau.OrchestrationSpec) (<-chan tau.SessionEvent, error) {
	return nil, errors.New("sdd test: noOpOrchestrator should never be invoked")
}

func (noOpOrchestrator) Err() error { return nil }

// Compile-time assertions that the helper types satisfy the SDK
// interfaces the tests rely on.
var (
	_ tau.ResponseObserver = (*captureObserver)(nil)
	_ tau.Orchestrator     = noOpOrchestrator{}
)

// TestE2E_RealSession_WiresAllEntryPoints is the canonical SDK
// contract test required by task 10.2. It constructs a real
// *tau.AgentSession via tau.CreateAgentSession with all four plugin
// entry points (Middleware + Tools + Commands + Orchestrator) wired,
// runs one turn via tau.NewFauxProvider, and asserts:
//
//	(a) CreateAgentSession accepted the options (err == nil).
//	(b) `validate_change` is in the registered tool names.
//	(c) The four slash commands (propose, explore, apply, archive)
//	    are discoverable via sess.SlashCommands().
//	(d) The sibling ResponseObserver (captureObserver) captures the
//	    post-mutation *Request and observes that ContextMutator
//	    injected the active change's proposal text into req.System.
func TestE2E_RealSession_WiresAllEntryPoints(t *testing.T) {
	root := newChangeRoot(t)
	ctx := context.Background()

	// Scaffold the active change so the ContextMutator has a
	// proposal.md to inject and the Orchestrator entry point has a
	// tasks.md to parse.
	if _, err := Propose(ctx, root, "real-session"); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })

	opts := RegisterOptions{
		ContextMutator:    true,
		DecisionsObserver: true,
		ActiveChange:      "real-session",
		ChangeRoot:        root,
		MaxContextBytes:   8192,
		TagSubsystem:      "spec-driven-development",
	}

	// Sibling observer that captures the post-mutation req.System.
	capture := &captureObserver{}

	// Wire the four slash commands into a registry the session can
	// report via SlashCommands().
	registry := tau.NewRegistry()
	for _, cmd := range Commands(opts, store) {
		registry.Register(cmd)
	}

	// Compose middleware: SDD's ContextMutator + DecisionsObserver,
	// followed by the capture observer.
	mw := append(Middleware(opts, store), any(capture))

	// Build a placeholder orchestrator so Options.Orchestrator is
	// non-nil. This satisfies the SDK contract that a parent session
	// be orchestrator-enabled; the runtime never invokes it.
	placeholder := noOpOrchestrator{}

	// (a) CreateAgentSession accepts the wired options.
	sess, err := tau.CreateAgentSession(ctx, tau.Options{
		Cwd:           root,
		Model:         "claude-opus-4-5-20251101",
		LLMClient:     tau.NewFauxProvider("ok from faux"),
		Tools:         append(tau.BuiltinTools(), Tools(store)...),
		Settings:      tau.DefaultSettings(),
		StateManager:  tau.NewInMemoryManager(root),
		Middleware:    mw,
		Store:         store,
		SlashCommands: registry,
		Orchestrator:  placeholder,
	})
	if err != nil {
		t.Fatalf("(a) CreateAgentSession: %v", err)
	}
	if sess == nil {
		t.Fatalf("(a) CreateAgentSession returned nil session with nil err")
	}
	t.Cleanup(func() { _ = sess.Shutdown(ctx) })

	// (b) `validate_change` is in the registered tool names.
	toolNames := sess.Tools()
	foundValidate := false
	for _, n := range toolNames {
		if n == "validate_change" {
			foundValidate = true
			break
		}
	}
	if !foundValidate {
		t.Errorf("(b) validate_change missing from registered tools: %v", toolNames)
	}

	// (c) The four slash commands are discoverable.
	cmdNames := sess.SlashCommands()
	for _, want := range []string{"propose", "explore", "apply", "archive"} {
		found := false
		for _, n := range cmdNames {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("(c) slash command %q missing from session.SlashCommands(): %v", want, cmdNames)
		}
	}

	// Drive one turn. RequestMutators fire before the LLM call;
	// ResponseObservers fire after — so capture.lastSystem will
	// reflect the ContextMutator's injection.
	if err := sess.Run(ctx, "Work on the change."); err != nil {
		t.Fatalf("sess.Run: %v", err)
	}

	// (d) The capture observer saw the ContextMutator's injection.
	capture.mu.Lock()
	observedSystem := capture.lastSystem
	capture.mu.Unlock()
	if len(observedSystem) == 0 {
		t.Fatalf("(d) capture observer saw no req.System blocks; mutator did not run")
	}
	first, ok := observedSystem[0].(tau.TextContent)
	if !ok {
		t.Fatalf("(d) observedSystem[0] is %T, want TextContent", observedSystem[0])
	}
	if !strings.Contains(first.Text, "proposal.md") {
		t.Errorf("(d) first system block missing 'proposal.md' marker; got:\n%s", first.Text)
	}
	if !strings.Contains(first.Text, "real-session") {
		t.Errorf("(d) first system block missing change name 'real-session'; got:\n%s", first.Text)
	}
}

// TestE2E_SDDOrchestrator_RunNoPanic exercises task 8.3: it builds an
// SDDOrchestrator against a real parent *tau.AgentSession (constructed
// via tau.CreateAgentSession with NewFauxProvider + NewInMemoryManager),
// asserts the parsed phase count and dependency edges, and drives Run
// to completion without panicking.
//
// The orchestrator spawns real child sessions; each child runs one
// faux-provider turn before being shut down. We don't assert on the
// events themselves — only on phase structure and panic-freedom.
func TestE2E_SDDOrchestrator_RunNoPanic(t *testing.T) {
	root := newChangeRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := Propose(ctx, root, "orch-run"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// Replace the stub tasks.md with three sections so we can assert
	// phase count (3) and dependency edges (1→2→3).
	writeFile(t, filepath.Join(root, "orch-run", "tasks.md"),
		"## 1. A\n- [ ] 1.1 todo\n"+
			"## 2. B\n- [ ] 2.1 todo\n"+
			"## 3. C\n- [ ] 3.1 todo\n")

	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })

	// Construct the parent session with a placeholder orchestrator
	// so Spawn (called by SequentialOrchestrator.runOnePhase) succeeds.
	parent, err := tau.CreateAgentSession(ctx, tau.Options{
		Cwd:          root,
		Model:        "claude-opus-4-5-20251101",
		LLMClient:    tau.NewFauxProvider("ok from faux child"),
		Tools:        tau.BuiltinTools(),
		Settings:     tau.DefaultSettings(),
		StateManager: tau.NewInMemoryManager(root),
		Store:        store,
		Orchestrator: noOpOrchestrator{},
	})
	if err != nil {
		t.Fatalf("CreateAgentSession parent: %v", err)
	}
	t.Cleanup(func() { _ = parent.Shutdown(ctx) })

	// Build the SDD orchestrator against the real parent.
	sddOrchAny, err := NewSDDOrchestratorFromRoot(parent, root, "orch-run")
	if err != nil {
		t.Fatalf("NewSDDOrchestratorFromRoot: %v", err)
	}
	sddOrch := sddOrchAny.(*sddOrchestrator)

	// Phase count and dependency edges.
	phases := sddOrch.Phases()
	if len(phases) != 3 {
		t.Fatalf("phases len = %d, want 3", len(phases))
	}
	wantNames := []string{"1. A", "2. B", "3. C"}
	for i, want := range wantNames {
		if phases[i].Name != want {
			t.Errorf("phases[%d].Name = %q, want %q", i, phases[i].Name, want)
		}
	}
	if len(phases[0].DependsOn) != 0 {
		t.Errorf("phases[0].DependsOn = %v, want empty", phases[0].DependsOn)
	}
	if len(phases[1].DependsOn) != 1 || phases[1].DependsOn[0] != "1. A" {
		t.Errorf("phases[1].DependsOn = %v, want [\"1. A\"]", phases[1].DependsOn)
	}
	if len(phases[2].DependsOn) != 1 || phases[2].DependsOn[0] != "2. B" {
		t.Errorf("phases[2].DependsOn = %v, want [\"2. B\"]", phases[2].DependsOn)
	}

	// Drive Run. Bound the run so the test cannot hang; drain the
	// channel so the orchestrator's goroutines complete.
	runDone := make(chan struct{})
	var runPanic any
	go func() {
		defer func() {
			if r := recover(); r != nil {
				runPanic = r
			}
			close(runDone)
		}()
		runCtx, runCancel := context.WithTimeout(ctx, 10*time.Second)
		defer runCancel()
		ch, runErr := sddOrch.Run(runCtx, tau.OrchestrationSpec{})
		if runErr == nil && ch != nil {
			for range ch {
			}
		}
	}()

	select {
	case <-runDone:
	case <-time.After(20 * time.Second):
		t.Fatalf("orchestrator Run did not terminate within 20s")
	}

	if runPanic != nil {
		t.Fatalf("orchestrator Run panicked: %v", runPanic)
	}
}
