// register_test.go — tests for the four embedder entry points in register.go.
//
// Verifies the spec scenarios for the entry-point surface:
//   - Each call returns fresh values (no shared pointers across calls).
//   - RegisterOptions zero-value produces an empty middleware slice.
//   - Each constructor returns the documented number of components.
//   - Compile-time type assertions hold (Command, HeadlessTool, etc.).
package sdd

import (
	"context"
	"errors"
	"reflect"
	"testing"

	tau "github.com/coevin/tau/pkg/tau"
)

func TestMiddleware_ZeroValueProducesEmpty(t *testing.T) {
	got := Middleware(RegisterOptions{}, nil)
	if len(got) != 0 {
		t.Errorf("zero-value Middleware len = %d, want 0 (%v)", len(got), got)
	}
}

func TestMiddleware_ContextMutatorOnly(t *testing.T) {
	got := Middleware(RegisterOptions{
		ContextMutator: true,
		ActiveChange:   "x",
		ChangeRoot:     t.TempDir(),
	}, nil)
	if len(got) != 1 {
		t.Fatalf("Middleware len = %d, want 1", len(got))
	}
	if _, ok := got[0].(*ContextMutator); !ok {
		t.Errorf("got[0] type = %T, want *ContextMutator", got[0])
	}
}

func TestMiddleware_DecisionsObserverOnly(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	got := Middleware(RegisterOptions{
		DecisionsObserver: true,
		TagSubsystem:      "spec-driven-development",
	}, store)
	if len(got) != 1 {
		t.Fatalf("Middleware len = %d, want 1", len(got))
	}
	if _, ok := got[0].(*DecisionsObserver); !ok {
		t.Errorf("got[0] type = %T, want *DecisionsObserver", got[0])
	}
}

func TestMiddleware_BothComponents(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	got := Middleware(RegisterOptions{
		ContextMutator:    true,
		DecisionsObserver: true,
		ActiveChange:      "x",
		ChangeRoot:        t.TempDir(),
	}, store)
	if len(got) != 2 {
		t.Fatalf("Middleware len = %d, want 2", len(got))
	}
}

func TestMiddleware_FreshInstancesPerCall(t *testing.T) {
	a := Middleware(RegisterOptions{ContextMutator: true, ActiveChange: "x", ChangeRoot: t.TempDir()}, nil)
	b := Middleware(RegisterOptions{ContextMutator: true, ActiveChange: "x", ChangeRoot: t.TempDir()}, nil)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("Middleware lens = %d/%d", len(a), len(b))
	}
	if a[0] == b[0] {
		t.Errorf("Middleware returned shared pointer across calls")
	}
}

func TestTools_ReturnsValidateTool(t *testing.T) {
	got := Tools(nil)
	if len(got) != 1 {
		t.Fatalf("Tools len = %d, want 1", len(got))
	}
	if got[0].Name() != "validate_change" {
		t.Errorf("Tools[0].Name = %q, want \"validate_change\"", got[0].Name())
	}
	// Compile-time type check.
	var _ tau.HeadlessTool = got[0]
}

func TestTools_FreshInstancesPerCall(t *testing.T) {
	a := Tools(nil)
	b := Tools(nil)
	if reflect.ValueOf(a[0]).Pointer() == reflect.ValueOf(b[0]).Pointer() {
		t.Errorf("Tools returned shared pointer across calls")
	}
}

func TestCommands_ReturnsFour(t *testing.T) {
	got := Commands(RegisterOptions{}, nil)
	if len(got) != 4 {
		t.Fatalf("Commands len = %d, want 4", len(got))
	}
	wantNames := map[string]bool{
		"/propose": false, "/explore": false, "/apply": false, "/archive": false,
	}
	for _, cmd := range got {
		wantNames[cmd.Name()] = true
		// Compile-time type check via interface assertion.
		var _ tau.Command = cmd
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("missing command %q", name)
		}
	}
}

func TestCommands_FreshInstancesPerCall(t *testing.T) {
	// Stateful commands (Propose, Apply, Archive) must not share
	// pointers across calls. ExploreCommand has no fields, so all
	// instances are structurally identical; the pointer comparison
	// is skipped for it because Go may alias zero-sized values.
	a := Commands(RegisterOptions{}, nil)
	b := Commands(RegisterOptions{}, nil)
	if len(a) != 4 || len(b) != 4 {
		t.Fatalf("Commands lens = %d/%d", len(a), len(b))
	}
	for i := range a {
		if a[i].Name() == "/explore" {
			continue
		}
		if reflect.ValueOf(a[i]).Pointer() == reflect.ValueOf(b[i]).Pointer() {
			t.Errorf("Commands[%d] (%s) shared pointer across calls", i, a[i].Name())
		}
	}
}

func TestOrchestrator_ErrorsWhenActiveChangeMissing(t *testing.T) {
	_, err := Orchestrator(nil, RegisterOptions{})
	if err == nil {
		t.Errorf("Orchestrator with empty ActiveChange: expected error")
	}
}

func TestOrchestrator_ErrorsWhenChangeDirMissing(t *testing.T) {
	root := t.TempDir()
	_, err := Orchestrator(nil, RegisterOptions{
		ActiveChange: "ghost",
		ChangeRoot:   root,
	})
	if !errors.Is(err, ErrTasksMissing) {
		t.Errorf("Orchestrator missing change: want ErrTasksMissing, got %v", err)
	}
}

// Verify each entry point accepts the documented signature shape
// without compilation errors. This is largely a smoke test; the
// substantive behavioral assertions live in the per-component tests.
func TestEntryPoints_AcceptExpectedShapes(t *testing.T) {
	store := newMemStore()
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()

	_ = Middleware(RegisterOptions{ContextMutator: true, ActiveChange: "x", ChangeRoot: root}, store)
	_ = Tools(store)
	_ = Commands(RegisterOptions{ActiveChange: "x", ChangeRoot: root}, store)
	_, _ = Orchestrator(nil, RegisterOptions{ActiveChange: "x", ChangeRoot: root})
}

// Stub to keep the context import alive (used in error message strings).
var _ = context.Background
