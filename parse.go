// parse.go — minimal markdown parser for SDD artifacts.
//
// The OpenSpec rule set (third-party/OpenSpec/src/core/validation/validator.ts)
// is line-anchored, not AST-based. It needs section lookup by `##` header,
// requirement-block extraction by `### Requirement:`, scenario counting by
// `####`, and checkbox parsing for `tasks.md` progress. We hand-roll those
// four primitives here using bufio.Scanner plus the four regexes the
// validator already uses. A markdown-AST library would be overkill and would
// add a dependency for ~30 lines of parsing code (design.md D5).
package sdd

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// SectionHeaderRE matches a level-2 (`##`) section header line at column 0.
// Captures the trimmed header text. Used by ParseSections.
//
// Matches the OpenSpec parser's requirement that headers be line-anchored
// (third-party/OpenSpec/src/core/parsers/spec-structure.ts:2-3).
var sectionHeaderRE = regexp.MustCompile(`^##\s+(.+?)\s*$`)

// RequirementHeaderRE matches `### Requirement: <name>` headers. Captures the
// trimmed requirement name. Case-insensitive to match the OpenSpec regex
// /^###\s*Requirement:\s*(.+)\s*$/i (validator.ts:415-441).
var requirementHeaderRE = regexp.MustCompile(`(?i)^###\s*Requirement:\s*(.+?)\s*$`)

// ScenarioHeaderRE matches `#### ` scenario headers. Used to count scenarios
// inside a requirement block. Mirrors /^####\s+/gm from validator.ts:466.
var scenarioHeaderRE = regexp.MustCompile(`(?m)^####\s+`)

// TaskLineRE matches a checkbox task line `- [ ]` / `- [x]` / `* [X]`.
// Matches task-progress.ts:4-5 and instructions.ts:305.
var taskLineRE = regexp.MustCompile(`(?m)^[-*]\s*\[([ xX])\]\s*(.+?)\s*$`)

// CodeFenceRE matches the opening of a fenced code block (``` or ~~~) so the
// section parser can treat lines inside the fence as plain text. Matches
// third-party/OpenSpec/src/core/parsers/spec-structure.ts:82.
var codeFenceRE = regexp.MustCompile(`^\s*(` + "`" + `{3,}|~{3,})`)

// MetadataLineRE matches metadata bullet lines like `**ID**:` or
// **Priority**:` and `**ID:**` (colon inside bold) that the
// requirement-text extractor must skip. Mirrors validator.ts:433.
var metadataLineRE = regexp.MustCompile(`^\*\*[^*]+:?\*\*:?\s*`)

// RequirementBlock is the parsed form of one `### Requirement:` block.
type RequirementBlock struct {
	// Name is the trimmed header text after "### Requirement:".
	Name string

	// RawBody is the verbatim block content from the `### Requirement:`
	// header line through the start of the next `###` header (or end of
	// input). Includes the header line itself and trailing newline.
	RawBody string

	// Scenarios is the count of `#### ` scenario headers in the block.
	Scenarios int
}

// Task is a single parsed checkbox entry from `tasks.md`.
type Task struct {
	// ID is the 1-based ordinal of the task within the parent section
	// body. Stable for a given input.
	ID int

	// Text is the trimmed task description (after `- [ ]` / `- [x]`).
	Text string

	// Done reports whether the box is checked.
	Done bool
}

// ParseSections walks the markdown body and returns the bodies of each
// `##`-level section keyed by trimmed header text. Lines inside fenced code
// blocks are treated as plain text so a `## not a header` inside a fence
// does not produce a spurious section.
//
// Mirrors the OpenSpec parser's stripping of fenced code blocks
// (third-party/OpenSpec/src/core/parsers/spec-structure.ts:76-105). The
// returned map does not preserve section order; callers that need order
// should use ParseSectionsOrdered.
func ParseSections(md []byte) (map[string]string, error) {
	out := map[string]string{}
	var currentName string
	var currentBody strings.Builder
	var inFence bool
	var fenceMarker byte

	flush := func() {
		if currentName != "" {
			out[currentName] = currentBody.String()
		}
		currentBody.Reset()
	}

	scanner := bufio.NewScanner(bytes.NewReader(md))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		raw := line

		// Track fenced code blocks. An opening fence starts a block;
		// the matching closing fence ends it. We mirror the OpenSpec
		// approach of stripping the fence while preserving the line so
		// column numbering is unaffected.
		if codeFenceRE.MatchString(raw) {
			marker := raw[strings.IndexAny(raw, "`~")]
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = 0
			}
			// Do not treat the fence line itself as a header candidate.
			if currentName != "" {
				currentBody.WriteString(raw)
				currentBody.WriteByte('\n')
			}
			continue
		}
		if inFence {
			if currentName != "" {
				currentBody.WriteString(raw)
				currentBody.WriteByte('\n')
			}
			continue
		}

		if m := sectionHeaderRE.FindStringSubmatch(raw); m != nil {
			flush()
			currentName = strings.TrimSpace(m[1])
			continue
		}
		if currentName != "" {
			currentBody.WriteString(raw)
			currentBody.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return out, nil
}

// ParseSectionsOrdered is like ParseSections but also returns the section
// names in source order. Used by the orchestrator (which walks `## N.`
// section headers in order) and by apply-phase progress reporting.
func ParseSectionsOrdered(md []byte) ([]string, map[string]string, error) {
	body := map[string]string{}
	var names []string
	var currentName string
	var currentBody strings.Builder
	var inFence bool
	var fenceMarker byte

	flush := func() {
		if currentName != "" {
			if _, exists := body[currentName]; !exists {
				names = append(names, currentName)
			}
			body[currentName] = currentBody.String()
		}
		currentBody.Reset()
	}

	scanner := bufio.NewScanner(bytes.NewReader(md))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		raw := line

		if codeFenceRE.MatchString(raw) {
			marker := raw[strings.IndexAny(raw, "`~")]
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = 0
			}
			if currentName != "" {
				currentBody.WriteString(raw)
				currentBody.WriteByte('\n')
			}
			continue
		}
		if inFence {
			if currentName != "" {
				currentBody.WriteString(raw)
				currentBody.WriteByte('\n')
			}
			continue
		}

		if m := sectionHeaderRE.FindStringSubmatch(raw); m != nil {
			flush()
			currentName = strings.TrimSpace(m[1])
			continue
		}
		if currentName != "" {
			currentBody.WriteString(raw)
			currentBody.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	flush()
	return names, body, nil
}

// ParseRequirements returns one RequirementBlock per `### Requirement:`
// header in sectionBody. The scenarios field counts `#### ` headers within
// the block's raw body (matching validator.ts:465-468).
//
// sectionBody is the body of a `## Requirements` (or delta) section, NOT the
// full markdown document. Callers obtain it from ParseSections.
func ParseRequirements(sectionBody []byte) ([]RequirementBlock, error) {
	var blocks []RequirementBlock
	var current *RequirementBlock
	var currentBody strings.Builder

	flush := func() {
		if current == nil {
			return
		}
		current.RawBody = currentBody.String()
		current.Scenarios = len(scenarioHeaderRE.FindAllString(currentBody.String(), -1))
		blocks = append(blocks, *current)
		current = nil
		currentBody.Reset()
	}

	scanner := bufio.NewScanner(bytes.NewReader(sectionBody))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := requirementHeaderRE.FindStringSubmatch(line); m != nil {
			flush()
			current = &RequirementBlock{Name: strings.TrimSpace(m[1])}
			currentBody.WriteString(line)
			currentBody.WriteByte('\n')
			continue
		}
		if current != nil {
			currentBody.WriteString(line)
			currentBody.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return blocks, nil
}

// ParseTasksSection returns one Task per `- [ ]` / `- [x]` checkbox line in
// the supplied section body. IDs are 1-based ordinals within the body.
//
// Mirrors task-progress.ts:4-5 and instructions.ts:305. Both `-` and `*`
// bullets are accepted; the box marker may be lower- or upper-case `x` for
// the checked state.
func ParseTasksSection(sectionBody []byte) ([]Task, error) {
	matches := taskLineRE.FindAllSubmatch(sectionBody, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	out := make([]Task, 0, len(matches))
	for i, m := range matches {
		marker := string(m[1])
		text := string(m[2])
		out = append(out, Task{
			ID:   i + 1,
			Text: strings.TrimSpace(text),
			Done: marker == "x" || marker == "X",
		})
	}
	return out, nil
}

// ExtractRequirementText returns the first non-blank, non-metadata,
// non-scenario-header line in blockRaw after the `### Requirement:` header
// line. This is the "requirement text" the validator tests for SHALL/MUST
// presence (validator.ts:415-441).
//
// Returns "" when no requirement text is found.
func ExtractRequirementText(blockRaw []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(blockRaw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			// Skip the header line itself.
			first = false
			continue
		}
		// Stop at the first scenario header; requirement text lives
		// above the scenarios.
		if strings.HasPrefix(strings.TrimSpace(line), "####") {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip metadata bullets like `**ID**:` / `**Priority**:`.
		if metadataLineRE.MatchString(trimmed) {
			continue
		}
		return trimmed
	}
	return ""
}
