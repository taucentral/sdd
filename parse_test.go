// parse_test.go — table-driven tests for the markdown parser.
package sdd

import (
	"strings"
	"testing"
)

func TestParseSections_BasicThreeSections(t *testing.T) {
	cases := []struct {
		name     string
		md       string
		want     map[string]string
	}{
		{
			name: "three sections, fenced code with header inside",
			md: "## Alpha\n" +
				"alpha body line 1\n" +
				"alpha body line 2\n" +
				"\n" +
				"## Beta\n" +
				"```\n" +
				"## not a header\n" +
				"```\n" +
				"\n" +
				"## Gamma\n" +
				"gamma body\n",
			want: map[string]string{
				"Alpha": "alpha body line 1\nalpha body line 2\n\n",
				"Beta":  "```\n## not a header\n```\n\n",
				"Gamma": "gamma body\n",
			},
		},
		{
			name: "single section",
			md:   "## Solo\nbody only\n",
			want: map[string]string{"Solo": "body only\n"},
		},
		{
			name: "no sections",
			md:   "just text\nno headers here\n",
			want: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSections([]byte(tc.md))
			if err != nil {
				t.Fatalf("ParseSections: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParseSections len = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("ParseSections[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestParseSections_FencedCodeDoesNotProduceSpuriousSection(t *testing.T) {
	md := "## Outside\n" +
		"\n" +
		"```go\n" +
		"// ## Comment inside code\n" +
		"// const X = 1\n" +
		"```\n" +
		"\n" +
		"## After\n" +
		"after body\n"
	got, err := ParseSections([]byte(md))
	if err != nil {
		t.Fatalf("ParseSections: %v", err)
	}
	if _, exists := got["Comment inside code"]; exists {
		t.Errorf("ParseSections picked up header inside fenced code block: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("ParseSections len = %d, want 2 (%v)", len(got), got)
	}
}

func TestParseRequirements_TwoBlocksScenarioCounts(t *testing.T) {
	body := "### Requirement: A\n" +
		"The system SHALL do A.\n" +
		"\n" +
		"### Requirement: B\n" +
		"The system SHALL do B.\n" +
		"\n" +
		"#### Scenario: first\n" +
		"- WHEN x\n" +
		"- THEN y\n" +
		"\n" +
		"#### Scenario: second\n" +
		"- WHEN z\n" +
		"\n" +
		"#### Scenario: third\n" +
		"- WHEN w\n"
	got, err := ParseRequirements([]byte(body))
	if err != nil {
		t.Fatalf("ParseRequirements: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseRequirements len = %d, want 2", len(got))
	}
	if got[0].Name != "A" || got[0].Scenarios != 0 {
		t.Errorf("got[0] = %+v, want Name=A Scenarios=0", got[0])
	}
	if got[1].Name != "B" || got[1].Scenarios != 3 {
		t.Errorf("got[1] = %+v, want Name=B Scenarios=3", got[1])
	}
}

func TestParseTasksSection_MixedChecked(t *testing.T) {
	body := "- [ ] first\n" +
		"- [x] second\n" +
		"* [X] third\n" +
		"- [ ] fourth\n" +
		"- [x] fifth\n"
	got, err := ParseTasksSection([]byte(body))
	if err != nil {
		t.Fatalf("ParseTasksSection: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("ParseTasksSection len = %d, want 5", len(got))
	}
	wantDone := []bool{false, true, true, false, true}
	for i, want := range wantDone {
		if got[i].Done != want {
			t.Errorf("got[%d].Done = %v, want %v", i, got[i].Done, want)
		}
		if got[i].ID != i+1 {
			t.Errorf("got[%d].ID = %d, want %d", i, got[i].ID, i+1)
		}
	}
}

func TestParseTasksSection_EmptyBodyReturnsNil(t *testing.T) {
	got, err := ParseTasksSection([]byte("nothing here\njust prose\n"))
	if err != nil {
		t.Fatalf("ParseTasksSection: %v", err)
	}
	if got != nil {
		t.Errorf("ParseTasksSection empty = %v, want nil", got)
	}
}

func TestExtractRequirementText_SkipsMetadataAndBlanks(t *testing.T) {
	block := "### Requirement: My Req\n" +
		"**ID:** REQ-001\n" +
		"**Priority:** high\n" +
		"\n" +
		"The system SHALL do the thing.\n" +
		"\n" +
		"#### Scenario: ok\n" +
		"- WHEN x\n"
	got := ExtractRequirementText([]byte(block))
	want := "The system SHALL do the thing."
	if got != want {
		t.Errorf("ExtractRequirementText = %q, want %q", got, want)
	}
}

func TestExtractRequirementText_StopsAtScenarioHeader(t *testing.T) {
	block := "### Requirement: X\n" +
		"\n" +
		"#### Scenario: only\n" +
		"- WHEN x\n" +
		"The system SHALL do X.\n" // body AFTER scenario; should not be picked
	got := ExtractRequirementText([]byte(block))
	if got != "" {
		t.Errorf("ExtractRequirementText = %q, want empty (no body before scenario)", got)
	}
}

func TestParseSectionsOrdered_PreservesOrder(t *testing.T) {
	md := "## C-first\n" +
		"c body\n" +
		"\n" +
		"## A-second\n" +
		"a body\n" +
		"\n" +
		"## B-third\n" +
		"b body\n"
	names, _, err := ParseSectionsOrdered([]byte(md))
	if err != nil {
		t.Fatalf("ParseSectionsOrdered: %v", err)
	}
	want := []string{"C-first", "A-second", "B-third"}
	if len(names) != len(want) {
		t.Fatalf("names len = %d, want %d (%v)", len(names), len(want), names)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestParseRequirements_EmptyBodyReturnsEmpty(t *testing.T) {
	got, err := ParseRequirements([]byte(""))
	if err != nil {
		t.Fatalf("ParseRequirements: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseRequirements empty = %v, want empty", got)
	}
}

// Ensure no whitespace prefix on requirement headers causes a miss.
func TestParseRequirements_RequiresColumnZeroHeader(t *testing.T) {
	body := "  ### Requirement: Indented\nbody\n"
	got, _ := ParseRequirements([]byte(body))
	if len(got) != 0 {
		t.Errorf("ParseSections accepted indented header: %v", got)
	}
}

// strings.TrimSpace here is just to keep the import alive; the parser
// does its own trimming via regex.
var _ = strings.TrimSpace
