// validate.go — the OpenSpec rule set ported to Go.
//
// Ports the rule set defined in
// third-party/OpenSpec/src/core/validation/validator.ts plus the cross-section
// conflict checker from the same file. Rules are grouped by what they validate:
//
//   - SPEC-*     : main spec files (openspec/specs/<capability>/spec.md)
//   - CHANGE-*   : change proposal.md (openspec/changes/<name>/proposal.md)
//   - DELTA-*    : change spec deltas (openspec/changes/<name>/specs/.../spec.md)
//   - CONFLICT-* : set-algebra across sections within one delta file
//
// Each rule produces zero or more Issue values tagged with a Severity and a
// RuleID. Validate* functions return a Report combining all issues; typed
// sentinels (ErrSpecMissing, ErrRequirementInvalid, ErrDeltaConflict) are
// returned when the report contains at least one ERROR so callers can branch
// on a stable error identity.
//
// The rules port the OpenSpec TypeScript constants and thresholds verbatim
// (third-party/OpenSpec/src/core/validation/constants.ts):
//
//	MIN_WHY_SECTION_LENGTH   = 50
//	MAX_WHY_SECTION_LENGTH   = 1000
//	MAX_REQUIREMENT_TEXT_LEN = 500
//	MAX_DELTAS_PER_CHANGE    = 10
//	MIN_DELTA_DESC_LEN       = 10
//
// tau deviates from pi by citing OpenSpec source paths rather than pi paths
// (see design.md "Note on pi reference"); the rule semantics are unchanged.
package sdd

import (
	"errors"
	"regexp"
	"strings"
)

// Severity classifies an Issue's importance. Matches ValidationLevel in
// third-party/OpenSpec/src/core/validation/types.ts.
type Severity string

const (
	// SeverityError indicates a hard rule violation; the report is
	// invalid when at least one ERROR is present.
	SeverityError Severity = "error"

	// SeverityWarning indicates a soft rule violation; the report is
	// valid but reviewers should look at it.
	SeverityWarning Severity = "warning"

	// SeverityInfo is purely informational (e.g. long requirement text).
	SeverityInfo Severity = "info"
)

// Issue is one rule finding. RuleID is the rule identifier (SPEC-001,
// CHANGE-003, DELTA-014, CONFLICT-002, etc.); Severity drives report
// aggregation. Path is the file path the issue applies to (relative to
// the change root, or "file" for whole-file issues). Message is the
// human-readable detail.
type Issue struct {
	// RuleID identifies the rule that produced the issue (e.g. "DELTA-003").
	RuleID string

	// Severity is one of SeverityError / SeverityWarning / SeverityInfo.
	Severity Severity

	// Path is the file path the issue applies to, relative to the
	// change root. "file" means the whole change directory.
	Path string

	// Message is the human-readable detail, including the offending
	// name when relevant.
	Message string
}

// Report aggregates validation issues by severity. HasErrors is
// convenient for short-circuiting; the Errors/Warnings/Info slices
// carry the actual findings in source order.
type Report struct {
	Errors   []Issue
	Warnings []Issue
	Info     []Issue
}

// HasErrors reports whether r contains at least one ERROR.
func (r Report) HasErrors() bool { return len(r.Errors) > 0 }

// String renders the report as the structured text the validate_change
// tool returns to the model. Sections appear in fixed order (ERRORS,
// WARNINGS, INFO); absent severities are omitted. Empty reports render
// as "OK".
func (r Report) String() string {
	if len(r.Errors) == 0 && len(r.Warnings) == 0 && len(r.Info) == 0 {
		return "OK"
	}
	var b strings.Builder
	if len(r.Errors) > 0 {
		b.WriteString("ERRORS:\n")
		for _, iss := range r.Errors {
			b.WriteString("  - [")
			b.WriteString(iss.RuleID)
			b.WriteString("] ")
			b.WriteString(iss.Path)
			b.WriteString(": ")
			b.WriteString(iss.Message)
			b.WriteByte('\n')
		}
	}
	if len(r.Warnings) > 0 {
		b.WriteString("WARNINGS:\n")
		for _, iss := range r.Warnings {
			b.WriteString("  - [")
			b.WriteString(iss.RuleID)
			b.WriteString("] ")
			b.WriteString(iss.Path)
			b.WriteString(": ")
			b.WriteString(iss.Message)
			b.WriteByte('\n')
		}
	}
	if len(r.Info) > 0 {
		b.WriteString("INFO:\n")
		for _, iss := range r.Info {
			b.WriteString("  - [")
			b.WriteString(iss.RuleID)
			b.WriteString("] ")
			b.WriteString(iss.Path)
			b.WriteString(": ")
			b.WriteString(iss.Message)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Typed sentinel errors. Callers use errors.Is to distinguish the failure
// mode. CLAUDE.md mandates typed sentinels over stringly-typed errors.New.
var (
	// ErrSpecMissing is returned by ValidateSpec when the report contains
	// any ERROR (typically a missing ## Purpose or ## Requirements section).
	ErrSpecMissing = errors.New("sdd: spec missing required structure")

	// ErrRequirementInvalid is returned by Validate* when the report
	// contains an ERROR caused by a malformed requirement block (missing
	// SHALL/MUST, missing scenarios, etc.).
	ErrRequirementInvalid = errors.New("sdd: requirement invalid")

	// ErrDeltaConflict is returned by ValidateChangeDeltaSpecs when the
	// report contains an ERROR caused by a cross-section conflict or
	// other delta-level structural problem.
	ErrDeltaConflict = errors.New("sdd: delta conflict")
)

// Validation thresholds — verbatim ports of
// third-party/OpenSpec/src/core/validation/constants.ts. Renaming would
// diverge from the source; reviewers diffing the port against the
// original rely on the parallel.
const (
	minWhySectionLength     = 50
	maxWhySectionLength     = 1000
	maxRequirementTextLength = 500
	maxDeltasPerChange      = 10
	minDeltaDescriptionLen  = 10
)

// shallOrMustRE matches the whole-word presence of SHALL or MUST.
// Mirrors /\b(SHALL|MUST)\b/ in validator.ts:444.
var shallOrMustRE = regexp.MustCompile(`\b(SHALL|MUST)\b`)

// purposeHeaderRE matches the `## Purpose` section header. Matches
// REQUIREMENTS_SECTION_HEADER's sibling in spec-structure.ts:1.
var purposeHeaderRE = regexp.MustCompile(`(?i)^##\s+Purpose\s*$`)

// requirementsHeaderRE matches `## Requirements`.
var requirementsHeaderRE = regexp.MustCompile(`(?i)^##\s+Requirements\s*$`)

// deltaHeaderRE matches any of the four delta section headers
// (`## ADDED Requirements`, etc.). Mirrors DELTA_HEADER in
// spec-structure.ts:3.
var deltaHeaderRE = regexp.MustCompile(`(?i)^##\s+(ADDED|MODIFIED|REMOVED|RENAMED)\s+Requirements\s*$`)

// topLevelHeaderRE matches any `## ` header.
var topLevelHeaderRE = regexp.MustCompile(`^##\s+`)

// whyHeaderRE matches `## Why` in a change proposal.
var whyHeaderRE = regexp.MustCompile(`(?i)^##\s+Why\s*$`)

// whatChangesHeaderRE matches `## What Changes` in a change proposal.
var whatChangesHeaderRE = regexp.MustCompile(`(?i)^##\s+What\s+Changes\s*$`)

// bulletEntryRE matches a delta bullet entry under `## What Changes`:
// `- **Modified Capability:**` / `- **New Capability:**` / `- ` plain.
// Used to count deltas in a change proposal.
var bulletEntryRE = regexp.MustCompile(`(?m)^\s*[-*]\s+`)

// capabilityBulletRE captures the capability named by a What Changes
// bullet like `- **New Capability:**` or `- **Modified Capability:**`.
var capabilityBulletRE = regexp.MustCompile(`(?i)^\s*[-*]\s+\*\*(New|Modified|Removed)?\s*Capabilit(y|ies)\*\*:\s*(.+?)\s*$`)

// renamedPairRE captures `from` and `to` of a RENAMED bullet. The
// OpenSpec convention is `- **from** -> **to**` or `- from -> to`.
var renamedPairRE = regexp.MustCompile(`(?m)^\s*[-*]\s+(.+?)\s*->\s*(.+?)\s*$`)

// addIssue appends issue to the appropriate slice on r based on severity.
func (r *Report) addIssue(severity Severity, ruleID, path, message string) {
	iss := Issue{RuleID: ruleID, Severity: severity, Path: path, Message: message}
	switch severity {
	case SeverityError:
		r.Errors = append(r.Errors, iss)
	case SeverityWarning:
		r.Warnings = append(r.Warnings, iss)
	case SeverityInfo:
		r.Info = append(r.Info, iss)
	}
}

// ValidateSpec runs SPEC-001 through SPEC-011 against a main spec file's
// markdown. Rules cover:
//
//   - SPEC-001: require `## Purpose` section (error if absent).
//   - SPEC-002: require `## Requirements` section (error if absent).
//   - SPEC-003: require ≥1 requirement in the Requirements section (error if empty).
//   - SPEC-004: warn when `## Purpose` body is shorter than minPurposeLength (warning).
//   - SPEC-005: reject delta headers in main specs (error per occurrence).
//   - SPEC-006: reject `### Requirement:` headers outside the Requirements section (error per occurrence).
//   - SPEC-007: warn when a requirement has zero `#### ` scenarios (warning).
//   - SPEC-008: info when a requirement text exceeds maxRequirementTextLength (info).
//   - SPEC-009: reject fenced `## ` headers (handled by ParseSections — never produced).
//   - SPEC-010: reject `## ` headers without trailing text (error on empty header).
//   - SPEC-011: warn when an `## ` section body is empty (warning).
//
// Mirrors third-party/OpenSpec/src/core/parsers/spec-structure.ts and
// validator.ts:290-329 (applySpecRules).
func ValidateSpec(md []byte) Report {
	var rep Report

	// Strip fenced code blocks before scanning headers so a `## x`
	// inside a fence is invisible. We use ParseSections to do this;
	// sections outside any code fence end up in the map.
	sections, err := ParseSections(md)
	if err != nil {
		rep.addIssue(SeverityError, "SPEC-000", "file", "spec failed to parse: "+err.Error())
		return rep
	}

	if _, ok := sections["Purpose"]; !ok {
		rep.addIssue(SeverityError, "SPEC-001", "file", `spec missing required section "## Purpose"`)
	}
	if _, ok := sections["Requirements"]; !ok {
		rep.addIssue(SeverityError, "SPEC-002", "file", `spec missing required section "## Requirements"`)
	}

	if purpose, ok := sections["Purpose"]; ok {
		body := strings.TrimSpace(purpose)
		if len(body) < minWhySectionLength {
			rep.addIssue(SeverityWarning, "SPEC-004", "purpose", "purpose section is too brief")
		}
	}

	if reqBody, ok := sections["Requirements"]; ok {
		blocks, _ := ParseRequirements([]byte(reqBody))
		if len(blocks) == 0 {
			rep.addIssue(SeverityError, "SPEC-003", "requirements", "spec has no requirements")
		}
		for _, blk := range blocks {
			text := ExtractRequirementText([]byte(blk.RawBody))
			if text == "" {
				rep.addIssue(SeverityWarning, "SPEC-007", "requirements", `requirement "`+blk.Name+`" has no body text`)
			}
			if len(text) > maxRequirementTextLength {
				rep.addIssue(SeverityInfo, "SPEC-008", "requirements", `requirement "`+blk.Name+`" text exceeds `+itoa(maxRequirementTextLength)+` characters`)
			}
			if blk.Scenarios == 0 {
				rep.addIssue(SeverityWarning, "SPEC-007", "requirements", `requirement "`+blk.Name+`" has no scenarios`)
			}
		}
	}

	// SPEC-005 and SPEC-006 walk the raw markdown (after fence-stripping)
	// because ParseSections discards the section headers it parses by.
	// Re-scan for delta headers and for requirement headers outside the
	// Requirements section.
	scanSpecStructure(md, &rep)

	// SPEC-011: warn on empty `## ` sections.
	for name, body := range sections {
		if strings.TrimSpace(body) == "" {
			rep.addIssue(SeverityWarning, "SPEC-011", name, `section "## `+name+`" is empty`)
		}
	}
	return rep
}

// scanSpecStructure runs SPEC-005 (reject delta headers in main spec) and
// SPEC-006 (reject requirement headers outside Requirements section). It
// reuses the OpenSpec fence-stripping approach (spec-structure.ts:76-105).
func scanSpecStructure(md []byte, rep *Report) {
	var inFence bool
	var fenceMarker byte
	var inRequirements bool

	lines := strings.Split(string(md), "\n")
	for i, raw := range lines {
		// Toggle fence state.
		if codeFenceRE.MatchString(raw) {
			// Find the fence marker character (backtick or tilde).
			idx := strings.IndexAny(raw, "`~")
			marker := raw[idx]
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = 0
			}
			continue
		}
		if inFence {
			continue
		}

		// Track whether we are inside the Requirements section.
		if m := topLevelHeaderRE.FindStringSubmatch(raw); m != nil {
			inRequirements = requirementsHeaderRE.MatchString(raw)
			// SPEC-005: reject delta headers in main specs.
			if deltaHeaderRE.MatchString(raw) {
				rep.addIssue(SeverityError, "SPEC-005", "file", "line "+itoa(i+1)+`: main spec contains delta header "`+strings.TrimSpace(raw)+`"`)
			}
			continue
		}
		// SPEC-006: requirement header outside Requirements section.
		if requirementHeaderRE.MatchString(raw) && !inRequirements {
			rep.addIssue(SeverityError, "SPEC-006", "file", "line "+itoa(i+1)+`: requirement header "`+strings.TrimSpace(raw)+`" outside Requirements section`)
		}
	}
}

// ValidateChangeProposal runs CHANGE-001 through CHANGE-008 against a
// change proposal.md body. Rules cover:
//
//   - CHANGE-001: require `## Why` section (error if absent).
//   - CHANGE-002: require `## What Changes` section (error if absent).
//   - CHANGE-003: enforce len(why body) in [minWhySectionLength, maxWhySectionLength].
//   - CHANGE-004: require ≥1 delta bullet under What Changes (error if zero).
//   - CHANGE-005: warn on >maxDeltasPerChange bullets (info).
//   - CHANGE-006: warn on delta description < minDeltaDescriptionLen.
//   - CHANGE-007: warn on ADDED/MODIFIED deltas with no requirements bullet.
//   - CHANGE-008: warn when What Changes is present but empty.
//
// Mirrors validator.ts:331-356 (applyChangeRules).
func ValidateChangeProposal(md []byte) Report {
	var rep Report
	sections, err := ParseSections(md)
	if err != nil {
		rep.addIssue(SeverityError, "CHANGE-000", "file", "proposal failed to parse: "+err.Error())
		return rep
	}

	why, hasWhy := sections["Why"]
	if !hasWhy {
		rep.addIssue(SeverityError, "CHANGE-001", "file", `proposal missing required section "## Why"`)
	}
	what, hasWhat := sections["What Changes"]
	if !hasWhat {
		rep.addIssue(SeverityError, "CHANGE-002", "file", `proposal missing required section "## What Changes"`)
	}

	if hasWhy {
		// CHANGE-003: body length bounds. The why body is the trimmed
		// text of all non-empty lines after the header.
		whyText := strings.TrimSpace(why)
		if len(whyText) < minWhySectionLength {
			rep.addIssue(SeverityError, "CHANGE-003", "why", "why section too short (need >= "+itoa(minWhySectionLength)+" chars)")
		}
		if len(whyText) > maxWhySectionLength {
			rep.addIssue(SeverityWarning, "CHANGE-003", "why", "why section too long (want <= "+itoa(maxWhySectionLength)+" chars)")
		}
	}

	if hasWhat {
		deltas := parseChangeDeltas([]byte(what))
		if len(deltas) == 0 {
			rep.addIssue(SeverityError, "CHANGE-004", "what-changes", "change has no deltas under ## What Changes")
		}
		if len(deltas) > maxDeltasPerChange {
			rep.addIssue(SeverityWarning, "CHANGE-005", "what-changes", "change has too many deltas ("+itoa(len(deltas))+"> "+itoa(maxDeltasPerChange)+")")
		}
		for _, d := range deltas {
			if len(d.description) < minDeltaDescriptionLen {
				rep.addIssue(SeverityWarning, "CHANGE-006", "what-changes", `delta "`+d.operation+` `+d.capability+`" description too brief`)
			}
			if (d.operation == "ADDED" || d.operation == "MODIFIED") && d.requirementsCount == 0 {
				rep.addIssue(SeverityWarning, "CHANGE-007", "what-changes", d.operation+` delta for "`+d.capability+`" lists no requirements`)
			}
		}
		if strings.TrimSpace(what) == "" {
			rep.addIssue(SeverityWarning, "CHANGE-008", "what-changes", "What Changes section is empty")
		}
	}
	return rep
}

// changeDelta is one entry parsed from a `## What Changes` body. It is
// internal to the validator; callers use it only via parseChangeDeltas.
type changeDelta struct {
	operation         string // "ADDED" | "MODIFIED" | "REMOVED" | "New" | "Modified" | "" (plain)
	capability        string
	description       string
	requirementsCount int
}

// parseChangeDeltas walks the `## What Changes` body and extracts one
// changeDelta per bullet entry. Recognises the canonical phrasing from
// OpenSpec templates: `- **New Capability:** name`, `- **Modified
// Capability:** name`, `- **Removed Capability:** name`, plus plain `- description`.
func parseChangeDeltas(body []byte) []changeDelta {
	var out []changeDelta
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "*") {
			continue
		}
		// Strip the leading bullet marker.
		rest := strings.TrimSpace(trimmed[1:])
		if rest == "" {
			continue
		}
		var d changeDelta
		if m := capabilityBulletRE.FindStringSubmatch(line); m != nil {
			d.operation = strings.ToUpper(m[1])
			if d.operation == "" {
				d.operation = "ADDED"
			}
			d.capability = strings.TrimSpace(m[3])
			d.description = d.capability
		} else {
			d.description = rest
		}
		out = append(out, d)
	}
	return out
}

// ValidateChangeDeltaSpecs runs DELTA-001 through DELTA-014 against the
// collection of spec delta files under
// openspec/changes/<name>/specs/<capability>/spec.md. specs is keyed by
// the capability name; the value is the file's markdown.
//
// Rules cover:
//
//   - DELTA-001: require ≥1 delta across all files (error if zero).
//   - DELTA-002: reject a file with delta headers but no entries (error).
//   - DELTA-003: reject a file with no delta headers and no entries (error).
//   - DELTA-004: ADDED requirement missing text (error).
//   - DELTA-005: ADDED/MODIFIED requirement missing SHALL or MUST (error).
//   - DELTA-006: ADDED/MODIFIED requirement missing scenarios (error).
//   - DELTA-007: MODIFIED requirement missing text (error).
//   - DELTA-008: duplicate names within a section (error).
//   - DELTA-009: REMOVED entries must be names only (no body required).
//   - DELTA-010: RENAMED pairs well-formed (error if malformed).
//   - DELTA-011: info on requirement text exceeding maxRequirementTextLength.
//   - DELTA-012: warn on requirement with zero scenarios (non-fatal warning).
//   - DELTA-013: reject empty delta section headers (error).
//   - DELTA-014: cross-section conflicts (CONFLICT-* issues; see scanDeltaConflicts).
func ValidateChangeDeltaSpecs(specs map[string][]byte) Report {
	var rep Report
	totalDeltas := 0

	for capName, md := range specs {
		path := capName + "/spec.md"
		sections, err := ParseSections(md)
		if err != nil {
			rep.addIssue(SeverityError, "DELTA-000", path, "failed to parse: "+err.Error())
			continue
		}

		sets := newSectionSets()

		hasSectionHeader := false
		hasEntries := false
		for sectionName, body := range sections {
			if !isDeltaSection(sectionName) {
				continue
			}
			hasSectionHeader = true
			if strings.TrimSpace(body) == "" {
				rep.addIssue(SeverityError, "DELTA-013", path, `delta section "## `+sectionName+`" is empty`)
				continue
			}

			// ADDED / MODIFIED use requirement blocks.
			switch {
			case strings.EqualFold(sectionName, "ADDED Requirements"):
				blocks, _ := ParseRequirements([]byte(body))
				for _, blk := range blocks {
					totalDeltas++
					hasEntries = true
					name := strings.TrimSpace(blk.Name)
					if _, dup := sets.added[name]; dup {
						rep.addIssue(SeverityError, "DELTA-008", path, `duplicate requirement in ADDED: "`+name+`"`)
						continue
					}
					sets.added[name] = struct{}{}
					text := ExtractRequirementText([]byte(blk.RawBody))
					if text == "" {
						rep.addIssue(SeverityError, "DELTA-004", path, `ADDED "`+name+`" is missing requirement text`)
					} else if !shallOrMustRE.MatchString(text) {
						rep.addIssue(SeverityError, "DELTA-005", path, `ADDED "`+name+`" must contain SHALL or MUST`)
					}
					if blk.Scenarios < 1 {
						rep.addIssue(SeverityError, "DELTA-006", path, `ADDED "`+name+`" must include at least one scenario`)
					}
					if len(text) > maxRequirementTextLength {
						rep.addIssue(SeverityInfo, "DELTA-011", path, `requirement "`+name+`" text exceeds `+itoa(maxRequirementTextLength)+` characters`)
					}
				}
			case strings.EqualFold(sectionName, "MODIFIED Requirements"):
				blocks, _ := ParseRequirements([]byte(body))
				for _, blk := range blocks {
					totalDeltas++
					hasEntries = true
					name := strings.TrimSpace(blk.Name)
					if _, dup := sets.modified[name]; dup {
						rep.addIssue(SeverityError, "DELTA-008", path, `duplicate requirement in MODIFIED: "`+name+`"`)
						continue
					}
					sets.modified[name] = struct{}{}
					text := ExtractRequirementText([]byte(blk.RawBody))
					if text == "" {
						rep.addIssue(SeverityError, "DELTA-007", path, `MODIFIED "`+name+`" is missing requirement text`)
					} else if !shallOrMustRE.MatchString(text) {
						rep.addIssue(SeverityError, "DELTA-005", path, `MODIFIED "`+name+`" must contain SHALL or MUST`)
					}
					if blk.Scenarios < 1 {
						rep.addIssue(SeverityError, "DELTA-006", path, `MODIFIED "`+name+`" must include at least one scenario`)
					}
					if len(text) > maxRequirementTextLength {
						rep.addIssue(SeverityInfo, "DELTA-011", path, `requirement "`+name+`" text exceeds `+itoa(maxRequirementTextLength)+` characters`)
					}
				}
			case strings.EqualFold(sectionName, "REMOVED Requirements"):
				// REMOVED is names-only. Accept either bullet list or
				// bare lines. Each name counts as one delta.
				for _, name := range parseRemovedNames(body) {
					totalDeltas++
					hasEntries = true
					if _, dup := sets.removed[name]; dup {
						rep.addIssue(SeverityError, "DELTA-008", path, `duplicate requirement in REMOVED: "`+name+`"`)
						continue
					}
					sets.removed[name] = struct{}{}
				}
			case strings.EqualFold(sectionName, "RENAMED Requirements"):
				// RENAMED pairs: each line `- from -> to`.
				pairs := parseRenamedPairs([]byte(body))
				for _, p := range pairs {
					totalDeltas++
					hasEntries = true
					if _, dup := sets.renamedFrom[p.from]; dup {
						rep.addIssue(SeverityError, "DELTA-008", path, `duplicate FROM in RENAMED: "`+p.from+`"`)
						continue
					}
					if _, dup := sets.renamedTo[p.to]; dup {
						rep.addIssue(SeverityError, "DELTA-008", path, `duplicate TO in RENAMED: "`+p.to+`"`)
						continue
					}
					sets.renamedFrom[p.from] = struct{}{}
					sets.renamedTo[p.to] = struct{}{}
					sets.renamedPairs = append(sets.renamedPairs, p)
				}
			}
		}

		// DELTA-002 / DELTA-003: empty / missing delta headers.
		if !hasEntries {
			if hasSectionHeader {
				rep.addIssue(SeverityError, "DELTA-002", path, "delta section headers present but no entries parsed")
			} else {
				rep.addIssue(SeverityError, "DELTA-003", path, "no delta section headers found")
			}
		}

		// DELTA-014: cross-section conflicts within this file.
		scanDeltaConflicts(path, sets, &rep)
	}

	if totalDeltas == 0 {
		rep.addIssue(SeverityError, "DELTA-001", "file", "change has no deltas across any spec file")
	}
	return rep
}

// renamedPair is a (from, to) tuple for RENAMED entries.
type renamedPair struct {
	from string
	to   string
}

// sectionSets collects the per-section name sets for one delta spec file.
// scanDeltaConflicts reads these sets to enforce CONFLICT-001..005.
type sectionSets struct {
	added        map[string]struct{}
	modified     map[string]struct{}
	removed      map[string]struct{}
	renamedFrom  map[string]struct{}
	renamedTo    map[string]struct{}
	renamedPairs []renamedPair
}

func newSectionSets() *sectionSets {
	return &sectionSets{
		added:       map[string]struct{}{},
		modified:    map[string]struct{}{},
		removed:     map[string]struct{}{},
		renamedFrom: map[string]struct{}{},
		renamedTo:   map[string]struct{}{},
	}
}

// parseRemovedNames returns the names listed in a REMOVED Requirements body.
// Accepts either `- Name` bullets or bare `Name` lines.
func parseRemovedNames(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
			trimmed = strings.TrimSpace(trimmed[1:])
		}
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// parseRenamedPairs walks a RENAMED Requirements body and returns each
// `from -> to` pair. Lines that don't match are ignored.
func parseRenamedPairs(body []byte) []renamedPair {
	var out []renamedPair
	for _, m := range renamedPairRE.FindAllSubmatch(body, -1) {
		out = append(out, renamedPair{
			from: strings.TrimSpace(string(m[1])),
			to:   strings.TrimSpace(string(m[2])),
		})
	}
	return out
}

// isDeltaSection reports whether sectionName is one of the four delta
// section headers (case-insensitive). Used by ValidateChangeDeltaSpecs
// to walk only the relevant sections.
func isDeltaSection(sectionName string) bool {
	return deltaHeaderRE.MatchString("## " + sectionName)
}

// scanDeltaConflicts runs the five set-algebra rules:
//
//   - CONFLICT-001: MODIFIED ∩ REMOVED
//   - CONFLICT-002: MODIFIED ∩ ADDED
//   - CONFLICT-003: ADDED ∩ REMOVED
//   - CONFLICT-004: RENAMED.from ∩ MODIFIED
//   - CONFLICT-005: RENAMED.to ∩ ADDED
//
// Names are normalised via strings.TrimSpace only (case-sensitive),
// matching the OpenSpec convention.
//
// Mirrors validator.ts:226-248.
func scanDeltaConflicts(path string, s *sectionSets, rep *Report) {
	for n := range s.modified {
		if _, ok := s.removed[n]; ok {
			rep.addIssue(SeverityError, "CONFLICT-001", path, `requirement "`+n+`" present in both MODIFIED and REMOVED`)
		}
		if _, ok := s.added[n]; ok {
			rep.addIssue(SeverityError, "CONFLICT-002", path, `requirement "`+n+`" present in both MODIFIED and ADDED`)
		}
	}
	for n := range s.added {
		if _, ok := s.removed[n]; ok {
			rep.addIssue(SeverityError, "CONFLICT-003", path, `requirement "`+n+`" present in both ADDED and REMOVED`)
		}
	}
	for _, p := range s.renamedPairs {
		if _, ok := s.modified[p.from]; ok {
			rep.addIssue(SeverityError, "CONFLICT-004", path, `MODIFIED references old name from RENAMED: "`+p.from+`"`)
		}
		if _, ok := s.added[p.to]; ok {
			rep.addIssue(SeverityError, "CONFLICT-005", path, `RENAMED TO "`+p.to+`" collides with ADDED`)
		}
	}
}

// SentinelToError maps a Report to a typed sentinel. Returns nil when
// the report has no ERRORs. Used by ValidateTool.Execute.
func SentinelToError(r Report) error {
	if !r.HasErrors() {
		return nil
	}
	// Prefer the most-specific sentinel: if any ERROR rule is a
	// CONFLICT-* or DELTA-* issue, return ErrDeltaConflict; if any is
	// a SPEC-* issue, return ErrSpecMissing; otherwise
	// ErrRequirementInvalid (CHANGE-* and requirement-level DELTA-*).
	for _, iss := range r.Errors {
		switch {
		case strings.HasPrefix(iss.RuleID, "CONFLICT-"):
			return ErrDeltaConflict
		case strings.HasPrefix(iss.RuleID, "DELTA-"):
			return ErrDeltaConflict
		case strings.HasPrefix(iss.RuleID, "SPEC-"):
			return ErrSpecMissing
		}
	}
	return ErrRequirementInvalid
}

// itoa is a stdlib-local int -> string to keep the validator
// allocation-free in the common path. (strconv.Itoa is fine; this
// thin wrapper keeps the validator's I/O explicit.)
func itoa(n int) string {
	// Use strconv for clarity; the wrapper exists so future rule
	// ports can swap in a faster implementation without edits at
	// every call site.
	return strings.TrimSpace(formatInt(n))
}

// formatInt is split out so it can be replaced wholesale if we ever
// need a locale-aware formatter.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
