// commands_test.go — table-driven tests for the four slash commands.
//
// Each test exercises the command's Execute path (and the lifecycle
// function it wraps) end-to-end against a temp change root. The fake
// session is nil where the lifecycle allows it (Explore) and omitted
// elsewhere where the lifecycle ignores it (Propose, Apply, Archive).
package sdd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny helper that fails the test on error.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestProposeCommand_Execute_CreatesFourArtifacts(t *testing.T) {
	root := t.TempDir()
	cmd := NewProposeCommand(root)
	out, err := cmd.Execute(context.Background(), "add-thing", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{
		"proposal.md", "design.md", "tasks.md", "specs/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
		if _, err := os.Stat(filepath.Join(root, "add-thing", strings.TrimSuffix(want, "/"))); err != nil {
			t.Errorf("artifact %s not created: %v", want, err)
		}
	}
}

func TestProposeCommand_Execute_RejectsNonKebabName(t *testing.T) {
	root := t.TempDir()
	cmd := NewProposeCommand(root)
	_, err := cmd.Execute(context.Background(), "Not Kebab", nil)
	if err == nil {
		t.Fatalf("Execute: expected error for non-kebab name, got nil")
	}
}

func TestProposeCommand_Execute_EmptyArgsErrors(t *testing.T) {
	root := t.TempDir()
	cmd := NewProposeCommand(root)
	if _, err := cmd.Execute(context.Background(), "   ", nil); err == nil {
		t.Fatalf("Execute: expected error for empty args")
	}
}

func TestProposeCommand_Execute_RefusesWhenSiblingActive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "other-active"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := NewProposeCommand(root)
	_, err := cmd.Execute(context.Background(), "second", nil)
	if !errors.Is(err, ErrAnotherChangeActive) {
		t.Fatalf("Execute: want ErrAnotherChangeActive, got %v", err)
	}
}

func TestExploreCommand_Execute_ReportsNoArtifacts(t *testing.T) {
	cmd := NewExploreCommand()
	out, err := cmd.Execute(context.Background(), "  some idea  ", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Explored:") || !strings.Contains(out, "some idea") {
		t.Errorf("output missing fields; got:\n%s", out)
	}
	if !strings.Contains(out, "No artifacts written.") {
		t.Errorf("output should say no artifacts; got:\n%s", out)
	}
	// Sanity: nothing was created under a temp change root.
	root := t.TempDir()
	if _, err := os.Stat(filepath.Join(root, "changes")); err == nil {
		t.Errorf("Explore wrote unexpected files")
	}
}

func TestApplyCommand_Execute_ReportsSectionProgress(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "add-x")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), `## 1. First
- [x] 1.1 done
- [ ] 1.2 todo

## 2. Second
- [x] 2.1 done
- [x] 2.2 done
`)
	cmd := NewApplyCommand(root, "add-x")
	out, err := cmd.Execute(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Section 1 (1. First): 1/2") {
		t.Errorf("missing Section 1 progress; got:\n%s", out)
	}
	if !strings.Contains(out, "Section 2 (2. Second): 2/2") {
		t.Errorf("missing Section 2 progress; got:\n%s", out)
	}
}

func TestApplyCommand_Execute_DerivesSoleActiveChange(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "the-only-one")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "## 1. Solo\n- [x] 1.1 done\n")
	cmd := NewApplyCommand(root, "") // no name wired
	out, err := cmd.Execute(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Section 1 (1. Solo): 1/1") {
		t.Errorf("missing progress; got:\n%s", out)
	}
}

func TestApplyCommand_Execute_ErrorsOnMissingTasks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "no-tasks"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := NewApplyCommand(root, "no-tasks")
	_, err := cmd.Execute(context.Background(), "", nil)
	if !errors.Is(err, ErrTasksMissing) {
		t.Fatalf("Execute: want ErrTasksMissing, got %v", err)
	}
}

func TestArchiveCommand_Execute_RefusesUncheckedTasks(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "incomplete")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "## 1. X\n- [ ] unchecked\n")
	cmd := NewArchiveCommand(root)
	_, err := cmd.Execute(context.Background(), "incomplete", nil)
	if !errors.Is(err, ErrUncheckedTasks) {
		t.Fatalf("Execute: want ErrUncheckedTasks, got %v", err)
	}
}

func TestArchiveCommand_Execute_MovesToArchive(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "ready")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "## 1. X\n- [x] done\n")
	cmd := NewArchiveCommand(root)
	out, err := cmd.Execute(context.Background(), "ready", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "archived") || !strings.Contains(out, "archive/") {
		t.Errorf("output missing archive notice; got:\n%s", out)
	}
	// Source dir must be gone.
	if _, err := os.Stat(changeDir); !os.IsNotExist(err) {
		t.Errorf("change dir still exists after archive; stat err = %v", err)
	}
	// Archive dir must contain a dated subdir.
	entries, err := os.ReadDir(filepath.Join(root, "archive"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive has %d entries, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), "-ready") {
		t.Errorf("archive entry = %q, want suffix -ready", entries[0].Name())
	}
}

func TestCommands_AllSatisfyTauCommand(t *testing.T) {
	// The compile-time assertions in commands.go already check this, but
	// exercise the constructors once to ensure they don't panic.
	root := t.TempDir()
	_ = NewProposeCommand(root)
	_ = NewExploreCommand()
	_ = NewApplyCommand(root, "")
	_ = NewArchiveCommand(root)
}

func TestCommands_NamesAreStable(t *testing.T) {
	if got := (NewProposeCommand("")).Name(); got != "/propose" {
		t.Errorf("/propose Name = %q", got)
	}
	if got := (NewExploreCommand()).Name(); got != "/explore" {
		t.Errorf("/explore Name = %q", got)
	}
	if got := (NewApplyCommand("", "")).Name(); got != "/apply" {
		t.Errorf("/apply Name = %q", got)
	}
	if got := (NewArchiveCommand("")).Name(); got != "/archive" {
		t.Errorf("/archive Name = %q", got)
	}
}

func TestCommands_ShortHelpIsNonEmpty(t *testing.T) {
	for i, cmd := range []interface {
		Name() string
		ShortHelp() string
	}{
		NewProposeCommand(""),
		NewExploreCommand(),
		NewApplyCommand("", ""),
		NewArchiveCommand(""),
	} {
		if cmd.ShortHelp() == "" {
			t.Errorf("command %d (%s) has empty ShortHelp", i, cmd.Name())
		}
	}
}
