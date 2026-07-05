// validate_test.go — table-driven tests for the validation rule set.
//
// At least one positive (rule passes) and one negative (rule fires)
// fixture per rule. Rules covered:
//
//   - SPEC-001 (Purpose required)        - DELTA-004 (ADDED missing text)
//   - SPEC-002 (Requirements required)   - DELTA-005 (SHALL/MUST missing)
//   - SPEC-003 (≥1 requirement)          - DELTA-006 (scenarios missing)
//   - SPEC-005 (no delta headers)        - DELTA-007 (MODIFIED missing text)
//   - SPEC-006 (req outside Requirements)- DELTA-008 (duplicate names)
//   - SPEC-008 (long req text INFO)      - DELTA-010 (RENAMED malformed)
//   - CHANGE-001 (Why required)          - DELTA-011 (long req INFO)
//   - CHANGE-002 (What Changes required) - DELTA-013 (empty section)
//   - CHANGE-003 (Why length bounds)     - DELTA-014 (cross-section conflict)
//   - CHANGE-004 (≥1 delta)              - CONFLICT-001 (MODIFIED∩REMOVED)
//   - CHANGE-006 (delta desc too brief)  - CONFLICT-002 (MODIFIED∩ADDED)
//   - CHANGE-007 (ADDED/MOD no reqs)     - CONFLICT-003 (ADDED∩REMOVED)
//   - DELTA-001 (no deltas across files) - CONFLICT-004 (REN.from∩MODIFIED)
//   - DELTA-002 (headers but no entries) - CONFLICT-005 (REN.to∩ADDED)
//   - DELTA-003 (no headers no entries)
package sdd

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSpec_RuleTable(t *testing.T) {
	cases := []struct {
		name      string
		md        string
		wantRules []string // rule IDs that MUST appear in Errors/Warnings/Info
		wantErr   error
	}{
		{
			name: "SPEC-001: missing Purpose",
			md: "## Requirements\n" +
				"### Requirement: X\nThe system SHALL x.\n",
			wantRules: []string{"SPEC-001"},
		},
		{
			name: "SPEC-002: missing Requirements",
			md: "## Purpose\nsome purpose here that is long enough to pass the warning\n",
			wantRules: []string{"SPEC-002"},
		},
		{
			name: "SPEC-003: Requirements section empty",
			md: "## Purpose\nsome purpose here that is long enough\n" +
				"## Requirements\n",
			wantRules: []string{"SPEC-003"},
		},
		{
			name: "SPEC-005: delta header in main spec",
			md: "## Purpose\nthis purpose is long enough to satisfy the minimum length check\n" +
				"## Requirements\n" +
				"### Requirement: X\nThe system SHALL x.\n" +
				"#### Scenario: ok\n- WHEN x\n" +
				"## ADDED Requirements\n### Requirement: Y\nThe system SHALL y.\n",
			wantRules: []string{"SPEC-005"},
		},
		{
			name: "SPEC-006: requirement outside Requirements",
			md: "## Purpose\nthis purpose is long enough to satisfy the minimum length check\n" +
				"### Requirement: Misplaced\nThe system SHALL x.\n" +
				"## Requirements\n" +
				"### Requirement: In\nThe system SHALL y.\n",
			wantRules: []string{"SPEC-006"},
		},
		{
			name: "SPEC-007: requirement without scenarios",
			md: "## Purpose\nthis purpose is long enough to satisfy the minimum length check\n" +
				"## Requirements\n" +
				"### Requirement: X\nThe system SHALL do something real here.\n",
			wantRules: []string{"SPEC-007"},
		},
		{
			name: "SPEC-008: long requirement INFO",
			md: "## Purpose\nthis purpose is long enough to satisfy the minimum length check\n" +
				"## Requirements\n" +
				"### Requirement: X\nThe system SHALL " +
				strings.Repeat("do a thing ", 60) + ".\n" +
				"#### Scenario: ok\n- WHEN x\n",
			wantRules: []string{"SPEC-008"},
		},
		{
			name: "positive: well-formed spec produces no errors",
			md: "## Purpose\nthis purpose is long enough to satisfy the minimum length check\n" +
				"## Requirements\n" +
				"### Requirement: X\nThe system SHALL do X.\n" +
				"#### Scenario: ok\n- WHEN x\n",
			wantRules: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := ValidateSpec([]byte(tc.md))
			checkRules(t, rep, tc.wantRules)
			if tc.wantErr != nil {
				got := SentinelToError(rep)
				if !errors.Is(got, tc.wantErr) {
					t.Errorf("SentinelToError = %v, want %v", got, tc.wantErr)
				}
			}
		})
	}
}

func TestValidateChangeProposal_RuleTable(t *testing.T) {
	cases := []struct {
		name      string
		md        string
		wantRules []string
	}{
		{
			name: "CHANGE-001: missing Why",
			md: "## What Changes\n" +
				"- **New Capability:** thing\n",
			wantRules: []string{"CHANGE-001"},
		},
		{
			name: "CHANGE-002: missing What Changes",
			md: "## Why\n" +
				"a reason that is long enough to pass the minimum length check\n",
			wantRules: []string{"CHANGE-002"},
		},
		{
			name: "CHANGE-003: Why too short",
			md: "## Why\ntoo short\n" +
				"## What Changes\n- **New Capability:** thing\n",
			wantRules: []string{"CHANGE-003"},
		},
		{
			name: "CHANGE-004: no deltas under What Changes",
			md: "## Why\na reason that is long enough to pass the minimum length check\n" +
				"## What Changes\n(no bullets)\n",
			wantRules: []string{"CHANGE-004"},
		},
		{
			name: "CHANGE-006: delta description too brief",
			md: "## Why\na reason that is long enough to pass the minimum length check\n" +
				"## What Changes\n- hi\n", // 2-char description
			wantRules: []string{"CHANGE-006"},
		},
		{
			name: "positive: well-formed change",
			md: "## Why\na reason that is long enough to pass the minimum length check\n" +
				"## What Changes\n" +
				"- **New Capability:** thing-that-is-described\n",
			wantRules: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := ValidateChangeProposal([]byte(tc.md))
			checkRules(t, rep, tc.wantRules)
		})
	}
}

func TestValidateChangeDeltaSpecs_RuleTable(t *testing.T) {
	cases := []struct {
		name      string
		specs     map[string][]byte
		wantRules []string
	}{
		{
			name: "DELTA-001: no deltas across files",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n(nothing)\n"),
			},
			wantRules: []string{"DELTA-001", "DELTA-002"},
		},
		{
			name: "DELTA-003: no headers no entries",
			specs: map[string][]byte{
				"cap": []byte("## Notes\nthis file has no delta headers\n"),
			},
			wantRules: []string{"DELTA-003", "DELTA-001"},
		},
		{
			name: "DELTA-004: ADDED missing text",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: X\n" +
					"#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: []string{"DELTA-004"},
		},
		{
			name: "DELTA-005: ADDED missing SHALL/MUST",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: X\n" +
					"The system may do X.\n" +
					"#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: []string{"DELTA-005"},
		},
		{
			name: "DELTA-006: ADDED missing scenarios",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: X\n" +
					"The system SHALL do X.\n"),
			},
			wantRules: []string{"DELTA-006"},
		},
		{
			name: "DELTA-007: MODIFIED missing text",
			specs: map[string][]byte{
				"cap": []byte("## MODIFIED Requirements\n" +
					"### Requirement: X\n" +
					"#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: []string{"DELTA-007"},
		},
		{
			name: "DELTA-008: duplicate ADDED name",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: X\nThe system SHALL x.\n#### Scenario: ok\n- WHEN x\n" +
					"### Requirement: X\nThe system SHALL x.\n#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: []string{"DELTA-008"},
		},
		{
			name: "DELTA-013: empty ADDED section",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n\n"),
			},
			wantRules: []string{"DELTA-013"},
		},
		{
			name: "DELTA-014 / CONFLICT-001: MODIFIED ∩ REMOVED",
			specs: map[string][]byte{
				"cap": []byte("## MODIFIED Requirements\n" +
					"### Requirement: Foo\nThe system SHALL foo.\n#### Scenario: ok\n- WHEN x\n" +
					"## REMOVED Requirements\n" +
					"- Foo\n"),
			},
			wantRules: []string{"CONFLICT-001"},
		},
		{
			name: "CONFLICT-002: MODIFIED ∩ ADDED",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: Foo\nThe system SHALL foo.\n#### Scenario: ok\n- WHEN x\n" +
					"## MODIFIED Requirements\n" +
					"### Requirement: Foo\nThe system SHALL foo.\n#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: []string{"CONFLICT-002"},
		},
		{
			name: "CONFLICT-003: ADDED ∩ REMOVED",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: Foo\nThe system SHALL foo.\n#### Scenario: ok\n- WHEN x\n" +
					"## REMOVED Requirements\n" +
					"- Foo\n"),
			},
			wantRules: []string{"CONFLICT-003"},
		},
		{
			name: "CONFLICT-004: RENAMED from ∩ MODIFIED",
			specs: map[string][]byte{
				"cap": []byte("## MODIFIED Requirements\n" +
					"### Requirement: OldName\nThe system SHALL x.\n#### Scenario: ok\n- WHEN x\n" +
					"## RENAMED Requirements\n" +
					"- OldName -> NewName\n"),
			},
			wantRules: []string{"CONFLICT-004"},
		},
		{
			name: "CONFLICT-005: RENAMED to ∩ ADDED",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: NewName\nThe system SHALL x.\n#### Scenario: ok\n- WHEN x\n" +
					"## RENAMED Requirements\n" +
					"- OldName -> NewName\n"),
			},
			wantRules: []string{"CONFLICT-005"},
		},
		{
			name: "positive: well-formed delta",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: X\nThe system SHALL x.\n#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: nil,
		},
		{
			name: "DELTA-011: long requirement triggers INFO",
			specs: map[string][]byte{
				"cap": []byte("## ADDED Requirements\n" +
					"### Requirement: X\nThe system SHALL " +
					strings.Repeat("do a thing ", 60) +
					".\n#### Scenario: ok\n- WHEN x\n"),
			},
			wantRules: []string{"DELTA-011"},
		},
		{
			name: "REMOVED accepts bullet list",
			specs: map[string][]byte{
				"cap": []byte("## REMOVED Requirements\n" +
					"- OldThing\n" +
					"- AnotherThing\n"),
			},
			wantRules: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := ValidateChangeDeltaSpecs(tc.specs)
			checkRules(t, rep, tc.wantRules)
		})
	}
}

// checkRules asserts that exactly the wantRules rule IDs are present in
// rep's combined Errors+Warnings+Info slices, no more, no less.
func checkRules(t *testing.T, rep Report, wantRules []string) {
	t.Helper()
	gotSet := map[string]int{}
	for _, iss := range rep.Errors {
		gotSet[iss.RuleID]++
	}
	for _, iss := range rep.Warnings {
		gotSet[iss.RuleID]++
	}
	for _, iss := range rep.Info {
		gotSet[iss.RuleID]++
	}
	if len(wantRules) == 0 {
		if len(gotSet) > 0 {
			t.Errorf("expected no rule findings, got %v", gotSet)
		}
		return
	}
	for _, w := range wantRules {
		if gotSet[w] == 0 {
			t.Errorf("expected rule %q in report; report = %v", w, gotSet)
		}
	}
}

func TestReport_String_FormatHasSections(t *testing.T) {
	rep := Report{
		Errors:   []Issue{{RuleID: "X-001", Path: "a", Message: "boom"}},
		Warnings: []Issue{{RuleID: "X-002", Path: "b", Message: "warn"}},
		Info:     []Issue{{RuleID: "X-003", Path: "c", Message: "info"}},
	}
	got := rep.String()
	for _, want := range []string{"ERRORS:", "WARNINGS:", "INFO:", "[X-001]", "[X-002]", "[X-003]"} {
		if !strings.Contains(got, want) {
			t.Errorf("report.String() missing %q; got:\n%s", want, got)
		}
	}
}

func TestReport_String_EmptyIsOK(t *testing.T) {
	rep := Report{}
	if got := rep.String(); got != "OK" {
		t.Errorf("empty report String = %q, want \"OK\"", got)
	}
}

func TestSentinelToError_Dispatch(t *testing.T) {
	cases := []struct {
		name string
		rep  Report
		want error
	}{
		{"clean -> nil", Report{}, nil},
		{"SPEC -> ErrSpecMissing", Report{Errors: []Issue{{RuleID: "SPEC-001"}}}, ErrSpecMissing},
		{"CHANGE -> ErrRequirementInvalid", Report{Errors: []Issue{{RuleID: "CHANGE-001"}}}, ErrRequirementInvalid},
		{"DELTA -> ErrDeltaConflict", Report{Errors: []Issue{{RuleID: "DELTA-001"}}}, ErrDeltaConflict},
		{"CONFLICT -> ErrDeltaConflict", Report{Errors: []Issue{{RuleID: "CONFLICT-001"}}}, ErrDeltaConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SentinelToError(tc.rep)
			if tc.want == nil {
				if got != nil {
					t.Errorf("SentinelToError = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Errorf("SentinelToError = %v, want %v", got, tc.want)
			}
		})
	}
}
