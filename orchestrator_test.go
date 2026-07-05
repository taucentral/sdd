// orchestrator_test.go — tests for the SDDOrchestrator wrapper.
//
// Exercises parsePhases (the core dependency-graph logic) and the
// constructor's three error paths. Run() is not exercised here because
// driving a real AgentSession is the job of the e2e_test.go suite.
package sdd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tau "github.com/coevin/tau/pkg/tau"
)

func TestParsePhases_ThreeSectionsInOrder(t *testing.T) {
	tasks := []byte("## 1. First\n- [ ] 1.1 todo\n" +
		"## 2. Second\n- [ ] 2.1 todo\n" +
		"## 3. Third\n- [ ] 3.1 todo\n")
	phases, err := parsePhases(tasks)
	if err != nil {
		t.Fatalf("parsePhases: %v", err)
	}
	if len(phases) != 3 {
		t.Fatalf("phases len = %d, want 3", len(phases))
	}
	wantNames := []string{"1. First", "2. Second", "3. Third"}
	for i, want := range wantNames {
		if phases[i].Name != want {
			t.Errorf("phases[%d].Name = %q, want %q", i, phases[i].Name, want)
		}
	}
	// First phase has no dependencies; subsequent phases depend on the
	// immediately preceding phase by Name.
	if len(phases[0].DependsOn) != 0 {
		t.Errorf("phases[0].DependsOn = %v, want empty", phases[0].DependsOn)
	}
	if len(phases[1].DependsOn) != 1 || phases[1].DependsOn[0] != "1. First" {
		t.Errorf("phases[1].DependsOn = %v, want [\"1. First\"]", phases[1].DependsOn)
	}
	if len(phases[2].DependsOn) != 1 || phases[2].DependsOn[0] != "2. Second" {
		t.Errorf("phases[2].DependsOn = %v, want [\"2. Second\"]", phases[2].DependsOn)
	}
}

func TestParsePhases_SingleSectionHasNoDeps(t *testing.T) {
	tasks := []byte("## 1. Solo\n- [ ] 1.1 todo\n")
	phases, err := parsePhases(tasks)
	if err != nil {
		t.Fatalf("parsePhases: %v", err)
	}
	if len(phases) != 1 {
		t.Fatalf("phases len = %d, want 1", len(phases))
	}
	if len(phases[0].DependsOn) != 0 {
		t.Errorf("phases[0].DependsOn = %v, want empty", phases[0].DependsOn)
	}
}

func TestParsePhases_EmptyBodyReturnsEmpty(t *testing.T) {
	phases, err := parsePhases([]byte(""))
	if err != nil {
		t.Fatalf("parsePhases: %v", err)
	}
	if len(phases) != 0 {
		t.Errorf("phases len = %d, want 0", len(phases))
	}
}

func TestParsePhases_PromptIncludesSectionNameAndBody(t *testing.T) {
	tasks := []byte("## 1. Things\n- [ ] 1.1 do stuff\n")
	phases, _ := parsePhases(tasks)
	if len(phases) != 1 {
		t.Fatalf("phases len = %d, want 1", len(phases))
	}
	prompt := phases[0].Prompt
	if !strings.Contains(prompt, "1. Things") {
		t.Errorf("prompt missing section name; got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "do stuff") {
		t.Errorf("prompt missing body content; got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Work on SDD section:") {
		t.Errorf("prompt missing standard header; got:\n%s", prompt)
	}
}

func TestNewSDDOrchestratorFromRoot_ErrorsOnMissingChangeDir(t *testing.T) {
	root := t.TempDir()
	_, err := NewSDDOrchestratorFromRoot(nil, root, "nope")
	if !errors.Is(err, ErrTasksMissing) {
		t.Errorf("NewSDDOrchestratorFromRoot missing dir: want ErrTasksMissing, got %v", err)
	}
}

func TestNewSDDOrchestratorFromRoot_ErrorsOnMissingTasksDotMd(t *testing.T) {
	root := t.TempDir()
	// changeDir exists but tasks.md does not.
	writeFile(t, filepath.Join(root, "nodir", ".keep"), "")
	_, err := NewSDDOrchestratorFromRoot(nil, root, "nodir")
	if !errors.Is(err, ErrTasksMissing) {
		t.Errorf("want ErrTasksMissing, got %v", err)
	}
}

func TestNewSDDOrchestratorFromRoot_ErrorsOnEmptyTasks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "empty", "tasks.md"), "no headers here\njust prose\n")
	_, err := NewSDDOrchestratorFromRoot(nil, root, "empty")
	if !errors.Is(err, ErrTasksUnparseable) {
		t.Errorf("want ErrTasksUnparseable, got %v", err)
	}
}

func TestNewSDDOrchestratorFromRoot_ParseSucceedsBeforeSDKCall(t *testing.T) {
	// tau.NewSequentialOrchestrator(nil) does not itself panic, but
	// the resulting orchestrator's Run returns ErrNoOrchestrator
	// because parent.Spawn refuses when parent is nil. That is by
	// design: a real sequential orchestrator cannot spawn children
	// without a parent.
	//
	// This test verifies only the SDD-side parsing layer. The
	// end-to-end Run path against a real *tau.AgentSession is
	// exercised in e2e_test.go (TestE2E_SDDOrchestrator_RunNoPanic).
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok", "tasks.md"),
		"## 1. A\n- [ ] 1.1 todo\n## 2. B\n- [ ] 2.1 todo\n")
	tasksBytes, err := os.ReadFile(filepath.Join(root, "ok", "tasks.md"))
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	phases, err := parsePhases(tasksBytes)
	if err != nil {
		t.Fatalf("parsePhases: %v", err)
	}
	if len(phases) != 2 {
		t.Errorf("phases len = %d, want 2", len(phases))
	}
}

func TestSDDOrchestrator_Run_NilInnerErrors(t *testing.T) {
	// Build an orchestrator with a nil inner to exercise the defensive
	// branch in Run().
	o := &sddOrchestrator{inner: nil}
	_, err := o.Run(context.Background(), tau.OrchestrationSpec{})
	if err == nil {
		t.Errorf("Run with nil inner: expected error, got nil")
	}
}

func TestSDDOrchestrator_Err_DelegatesToInner(t *testing.T) {
	// When inner is nil, Err() returns nil rather than panicking.
	o := &sddOrchestrator{inner: nil}
	if err := o.Err(); err != nil {
		t.Errorf("Err() with nil inner = %v, want nil", err)
	}
}

func TestBuildPhasePrompt_Form(t *testing.T) {
	got := buildPhasePrompt("3. Implementation", "- [ ] 3.1 thing\n")
	if !strings.HasPrefix(got, "Work on SDD section:") {
		t.Errorf("missing header prefix; got:\n%s", got)
	}
	if !strings.Contains(got, "3. Implementation") {
		t.Errorf("missing section name; got:\n%s", got)
	}
	if !strings.Contains(got, "3.1 thing") {
		t.Errorf("missing body; got:\n%s", got)
	}
}

// Compile-time assertion: *sddOrchestrator satisfies tau.Orchestrator.
var _ tau.Orchestrator = (*sddOrchestrator)(nil)
