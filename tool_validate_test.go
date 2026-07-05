// tool_validate_test.go — table-driven tests for the validate_change tool.
//
// Exercises both the happy path (returns a populated TextResult with
// ERRORS/WARNINGS/INFO markers) and the failure paths (missing change,
// malformed args). Tool results carry application errors as IsError=true;
// the only `error` return values come from context cancellation.
package sdd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tau "github.com/coevin/tau/pkg/tau"
)

// writeChange builds a minimal openspec/changes/<name>/ layout with the
// given proposal body and a single capability spec under specs/.
func writeChange(t *testing.T, root, name, proposal, specBody string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "proposal.md"), proposal)
	if specBody != "" {
		writeFile(t, filepath.Join(root, name, "specs", "cap", "spec.md"), specBody)
	}
}

func TestValidateTool_NameAndDescription(t *testing.T) {
	tool := NewValidateTool("")
	if tool.Name() != "validate_change" {
		t.Errorf("Name = %q, want \"validate_change\"", tool.Name())
	}
	if !strings.Contains(tool.Description(), "OpenSpec") {
		t.Errorf("Description missing 'OpenSpec': %q", tool.Description())
	}
}

func TestValidateTool_ParametersHasChangeProperty(t *testing.T) {
	tool := NewValidateTool("")
	schema := tool.Parameters()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if !strings.Contains(string(raw), `"change"`) {
		t.Errorf("schema missing 'change' property; schema = %s", raw)
	}
}

func TestValidateTool_Execute_HappyPath(t *testing.T) {
	root := t.TempDir()
	writeChange(t, root, "add-cap",
		"## Why\nthis reason is long enough to pass the minimum length check\n"+
			"## What Changes\n- **New Capability:** thing-described\n",
		"## ADDED Requirements\n"+
			"### Requirement: X\nThe system SHALL x.\n"+
			"#### Scenario: ok\n- WHEN x\n")
	tool := NewValidateTool(root)
	args, _ := json.Marshal(map[string]string{"change": "add-cap"})
	res, err := tool.Execute(context.Background(), tau.ToolCall{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute IsError=true; content=%v", res.Content)
	}
	tc, ok := res.Content[0].(tau.TextContent)
	if !ok {
		t.Fatalf("Content[0] = %T, want TextContent", res.Content[0])
	}
	if strings.Contains(tc.Text, "ERRORS:") && strings.Contains(tc.Text, "[SPEC-") {
		t.Errorf("expected clean report, got:\n%s", tc.Text)
	}
}

func TestValidateTool_Execute_MissingChangeIsError(t *testing.T) {
	root := t.TempDir()
	tool := NewValidateTool(root)
	args, _ := json.Marshal(map[string]string{"change": "nonexistent"})
	res, err := tool.Execute(context.Background(), tau.ToolCall{Args: args})
	if err != nil {
		t.Fatalf("Execute returned non-nil error: %v", err)
	}
	if !res.IsError {
		t.Errorf("Execute IsError=false for missing change; content=%v", res.Content)
	}
}

func TestValidateTool_Execute_MalformedArgsIsError(t *testing.T) {
	root := t.TempDir()
	tool := NewValidateTool(root)
	res, err := tool.Execute(context.Background(), tau.ToolCall{Args: json.RawMessage("not-json")})
	if err != nil {
		t.Fatalf("Execute returned non-nil error: %v", err)
	}
	if !res.IsError {
		t.Errorf("Execute IsError=false for malformed JSON")
	}
}

func TestValidateTool_Execute_SurfacesRuleFindings(t *testing.T) {
	root := t.TempDir()
	// Deliberately invalid: empty Why section + empty spec delta.
	writeChange(t, root, "broken",
		"## Why\nshort\n## What Changes\n- x\n",
		"## ADDED Requirements\n\n")
	tool := NewValidateTool(root)
	args, _ := json.Marshal(map[string]string{"change": "broken"})
	res, _ := tool.Execute(context.Background(), tau.ToolCall{Args: args})
	if res.IsError {
		t.Fatalf("Execute IsError=true for invalid-but-present change; content=%v", res.Content)
	}
	tc, ok := res.Content[0].(tau.TextContent)
	if !ok {
		t.Fatalf("Content[0] = %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "ERRORS:") {
		t.Errorf("expected ERRORS section in report; got:\n%s", tc.Text)
	}
}

func TestValidateTool_Execute_DefaultsChangeRootWhenEmpty(t *testing.T) {
	// With an empty changeRoot and no on-disk layout, the tool should
	// fail gracefully with IsError=true (rather than panic). This
	// exercises the defaultChangeRoot() path without depending on a
	// real openspec/ tree.
	tool := NewValidateTool("")
	args, _ := json.Marshal(map[string]string{"change": "nope-nope"})
	res, err := tool.Execute(context.Background(), tau.ToolCall{Args: args})
	if err != nil {
		t.Fatalf("Execute returned non-nil error: %v", err)
	}
	if !res.IsError {
		t.Errorf("Execute IsError=false; expected graceful failure")
	}
}

func TestValidateTool_Execute_RespectsContextCancellation(t *testing.T) {
	root := t.TempDir()
	writeChange(t, root, "ok",
		"## Why\nlong enough reason to pass the minimum length gate\n"+
			"## What Changes\n- **New Capability:** thing-described\n",
		"")
	tool := NewValidateTool(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args, _ := json.Marshal(map[string]string{"change": "ok"})
	_, err := tool.Execute(ctx, tau.ToolCall{Args: args})
	if err == nil {
		t.Errorf("Execute with cancelled ctx: expected error, got nil")
	}
}

func TestReadChangeSpecs_PicksUpAllSpecFiles(t *testing.T) {
	root := t.TempDir()
	changeDir := filepath.Join(root, "multi")
	writeFile(t, filepath.Join(changeDir, "specs", "alpha", "spec.md"),
		"## ADDED Requirements\n### Requirement: A\nThe system SHALL a.\n#### Scenario: s\n- WHEN a\n")
	writeFile(t, filepath.Join(changeDir, "specs", "beta", "spec.md"),
		"## ADDED Requirements\n### Requirement: B\nThe system SHALL b.\n#### Scenario: s\n- WHEN b\n")
	specs, errs := readChangeSpecs(changeDir)
	if len(errs) > 0 {
		t.Fatalf("readChangeSpecs errors: %v", errs)
	}
	if len(specs) != 2 {
		t.Fatalf("readChangeSpecs len = %d, want 2 (%v)", len(specs), specs)
	}
	for _, key := range []string{"alpha", "beta"} {
		if _, ok := specs[key]; !ok {
			t.Errorf("specs missing %q", key)
		}
	}
}

// Compile-time assertion: the tool satisfies tau.HeadlessTool.
var _ tau.HeadlessTool = (*ValidateTool)(nil)

// Ensure we touched os.Stat in this file (readChangeSpecs is exercised
// via the path-based fixtures above; keep the os import alive for the
// tempDir helpers).
var _ = os.Stat
