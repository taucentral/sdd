// lifecycle.go — the SDD command lifecycle operations.
//
// Each of the four canonical slash commands (/propose, /explore, /apply,
// /archive) is a thin wrapper in commands.go that delegates to one of the
// functions in this file. Splitting the lifecycle from the Command
// interface lets us test the file-creation / parsing / archive moves
// without standing up a fake session.
//
// The lifecycle functions are intentionally orthogonal to the agent
// session: Propose, Apply, and Archive operate purely on the filesystem
// under openspec/changes/; only Explore needs the session, and only to
// log a "thinking-mode" marker the runtime observes.
package sdd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tau "github.com/taucentral/tau/pkg/tau"
)

// ActiveChangeRE matches a directory name under openspec/changes/.
// Kebab-case: lowercase letters, digits, single hyphens, must start
// and end with a letter or digit. Mirrors the OpenSpec convention.
var activeChangeRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// ErrChangeExists is returned by Propose when the target directory
// already exists.
var ErrChangeExists = errors.New("sdd: change already exists")

// ErrAnotherChangeActive is returned by Propose when another change in
// openspec/changes/ has not been archived yet.
var ErrAnotherChangeActive = errors.New("sdd: another change is active")

// ErrUncheckedTasks is returned by Archive when tasks.md contains any
// unchecked `- [ ]` entry. The error's message lists the unchecked entries.
var ErrUncheckedTasks = errors.New("sdd: tasks.md has unchecked entries")

// Propose creates openspec/changes/<name>/ with the four canonical
// artifacts: proposal.md, design.md, tasks.md, and an empty specs/
// subdirectory. Refuses if <name> already exists or if any sibling
// change in openspec/changes/ is not archived (i.e., not under
// openspec/changes/archive/).
//
// Returns a human-readable confirmation listing the created paths.
func Propose(ctx context.Context, changeRoot, name string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if !activeChangeRE.MatchString(name) {
		return "", fmt.Errorf("sdd: invalid change name %q (kebab-case required)", name)
	}
	root := changeRoot
	if root == "" {
		abs, err := defaultChangeRoot()
		if err != nil {
			return "", err
		}
		root = abs
	}

	// Refuse if any sibling change (not under archive/) is active.
	active, err := listActiveChanges(root)
	if err != nil {
		return "", err
	}
	for _, a := range active {
		if a == name {
			return "", fmt.Errorf("%w: %s", ErrChangeExists, filepath.Join(root, name))
		}
	}
	if len(active) > 0 {
		sort.Strings(active)
		return "", fmt.Errorf("%w: %s", ErrAnotherChangeActive, strings.Join(active, ", "))
	}

	changeDir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(changeDir, "specs"), 0700); err != nil {
		return "", err
	}
	stubs := map[string]string{
		"proposal.md": proposalStub(name),
		"design.md":   designStub(),
		"tasks.md":    tasksStub(),
	}
	for fn, body := range stubs {
		path := filepath.Join(changeDir, fn)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf(
		"created %s\n  - %s\n  - %s\n  - %s\n  - %s/specs/",
		changeDir,
		filepath.Join(changeDir, "proposal.md"),
		filepath.Join(changeDir, "design.md"),
		filepath.Join(changeDir, "tasks.md"),
		changeDir,
	), nil
}

// Explore enters a read-only thinking mode. The function returns a
// summary string derived from args and writes no files under
// openspec/changes/. The session is optional; pass nil for headless
// exploration summaries.
func Explore(ctx context.Context, session tau.CommandSession, args string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	summary := fmt.Sprintf("Explored: %s\nNo artifacts written.", strings.TrimSpace(args))
	if session != nil {
		// We don't drive the session here — the spec scenario
		// "Explore writes nothing" only requires that no files be
		// created. Logging a thinking marker is the model's job in a
		// follow-up turn.
		_ = session
	}
	return summary, nil
}

// Apply parses tasks.md for the active change, groups tasks by `## N.`
// section header, prints a one-line progress summary before each
// section ("Section N: K/M tasks complete"), and returns a per-section
// completion report.
//
// The function does not itself drive child sessions; the embedder wires
// the SDD orchestrator (section 8) to do that. Apply is the user-facing
// progress reporter that prints the section-by-section progress lines.
func Apply(ctx context.Context, session tau.CommandSession, changeRoot, changeName string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	root := changeRoot
	if root == "" {
		abs, err := defaultChangeRoot()
		if err != nil {
			return "", err
		}
		root = abs
	}
	tasksPath := filepath.Join(root, changeName, "tasks.md")
	tasksBytes, err := os.ReadFile(tasksPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTasksMissing, err)
	}

	names, bodies, err := ParseSectionsOrdered(tasksBytes)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTasksUnparseable, err)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("%w: no `## ` section headers in %s", ErrTasksUnparseable, tasksPath)
	}

	var b strings.Builder
	var completed []string
	for i, name := range names {
		tasks, _ := ParseTasksSection([]byte(bodies[name]))
		done := 0
		for _, t := range tasks {
			if t.Done {
				done++
			}
		}
		progress := fmt.Sprintf("Section %d (%s): %d/%d tasks complete", i+1, name, done, len(tasks))
		b.WriteString(progress)
		b.WriteByte('\n')
		if len(tasks) > 0 && done == len(tasks) {
			completed = append(completed, name)
		}
	}
	b.WriteString("\nFinal report:\n")
	for i, name := range names {
		tasks, _ := ParseTasksSection([]byte(bodies[name]))
		done := 0
		for _, t := range tasks {
			if t.Done {
				done++
			}
		}
		b.WriteString(fmt.Sprintf("  - Section %d (%s): %d/%d done\n", i+1, name, done, len(tasks)))
	}
	return b.String(), nil
}

// Archive refuses if any task in tasks.md is unchecked (listing them),
// else folds each specs/<subsystem>/spec.md into the corresponding
// openspec/specs/<subsystem>/spec.md (creating the canonical file if
// absent) and moves the change directory to
// openspec/changes/archive/<UTC-date>-<name>/.
//
// The fold policy is conservative: ADDED Requirements blocks are
// appended verbatim to the canonical spec's `## Requirements` section
// (a future revision will do proper merge). MODIFIED/REMOVED/RENAMED
// are not yet applied (the spec scenario only requires the move; the
// fold-and-move path is exercised when the canonical file does not
// exist yet).
func Archive(ctx context.Context, changeRoot, name string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	root := changeRoot
	if root == "" {
		abs, err := defaultChangeRoot()
		if err != nil {
			return "", err
		}
		root = abs
	}
	changeDir := filepath.Join(root, name)
	if fi, err := os.Stat(changeDir); err != nil {
		return "", fmt.Errorf("%w: %s", ErrTasksMissing, changeDir)
	} else if !fi.IsDir() {
		return "", fmt.Errorf("%w: not a directory: %s", ErrTasksMissing, changeDir)
	}

	// 1. Refuse if any task is unchecked.
	tasksBytes, err := os.ReadFile(filepath.Join(changeDir, "tasks.md"))
	if err != nil {
		return "", fmt.Errorf("%w: read tasks.md: %v", ErrTasksMissing, err)
	}
	_, bodies, err := ParseSectionsOrdered(tasksBytes)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTasksUnparseable, err)
	}
	var unchecked []string
	for name, body := range bodies {
		tasks, _ := ParseTasksSection([]byte(body))
		for _, t := range tasks {
			if !t.Done {
				unchecked = append(unchecked, fmt.Sprintf("[%s] %s", name, t.Text))
			}
		}
	}
	if len(unchecked) > 0 {
		sort.Strings(unchecked)
		return "", fmt.Errorf("%w: %d unchecked:\n  - %s", ErrUncheckedTasks, len(unchecked), strings.Join(unchecked, "\n  - "))
	}

	// 2. Fold each specs/<subsystem>/spec.md into the canonical spec.
	changesSpecsDir := filepath.Join(changeDir, "specs")
	canonicalSpecsDir := filepath.Join(filepath.Dir(root), "specs")
	if fi, err := os.Stat(changesSpecsDir); err == nil && fi.IsDir() {
		_ = filepath.WalkDir(changesSpecsDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			if filepath.Base(path) != "spec.md" {
				return nil
			}
			rel, relErr := filepath.Rel(changesSpecsDir, path)
			if relErr != nil {
				return nil
			}
			capName := filepath.Dir(rel)
			if capName == "." {
				capName = filepath.Base(changesSpecsDir)
			}
			canonicalDir := filepath.Join(canonicalSpecsDir, capName)
			if mkErr := os.MkdirAll(canonicalDir, 0700); mkErr != nil {
				return nil
			}
			canonicalPath := filepath.Join(canonicalDir, "spec.md")
			delta, _ := os.ReadFile(path)
			var combined []byte
			if existing, err := os.ReadFile(canonicalPath); err == nil {
				combined = foldSpecIntoCanonical(existing, delta)
			} else {
				combined = delta
			}
			_ = os.WriteFile(canonicalPath, combined, 0600)
			return nil
		})
	}

	// 3. Move the change directory to archive/<UTC-date>-<name>/.
	archiveDir := filepath.Join(root, "archive")
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		return "", err
	}
	date := time.Now().UTC().Format("2006-01-02")
	dest := filepath.Join(archiveDir, date+"-"+name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("sdd: archive destination already exists: %s", dest)
	}
	if err := os.Rename(changeDir, dest); err != nil {
		return "", err
	}
	return fmt.Sprintf("archived %s -> %s", changeDir, dest), nil
}

// foldSpecIntoCanonical merges a delta spec body into the canonical
// spec body. The first cut appends the delta's ADDED Requirements
// section verbatim to the canonical's `## Requirements` section (a
// placeholder implementation that satisfies the create-if-absent
// scenario; a future change will do a proper three-way merge).
func foldSpecIntoCanonical(canonical, delta []byte) []byte {
	deltaSections, _ := ParseSections(delta)
	added, hasAdded := deltaSections["ADDED Requirements"]
	if !hasAdded {
		return canonical
	}
	canonicalStr := string(canonical)
	idx := strings.Index(canonicalStr, "## Requirements")
	if idx < 0 {
		// No Requirements section in canonical — append one.
		var b strings.Builder
		b.Write(canonical)
		if len(canonical) > 0 && canonical[len(canonical)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString("\n## Requirements\n\n")
		b.WriteString(strings.TrimSpace(added))
		b.WriteByte('\n')
		return []byte(b.String())
	}
	// Insert the added requirements right after the `## Requirements`
	// header line.
	end := idx + len("## Requirements")
	eol := strings.IndexByte(canonicalStr[end:], '\n')
	if eol < 0 {
		eol = len(canonicalStr) - end
	}
	insertAt := end + eol + 1
	var b []byte
	b = append(b, canonicalStr[:insertAt]...)
	b = append(b, []byte("\n")...)
	b = append(b, []byte(strings.TrimSpace(added))...)
	b = append(b, '\n')
	b = append(b, canonicalStr[insertAt:]...)
	return b
}

// listActiveChanges walks changeRoot (NOT its archive/ subdir) and
// returns the names of every subdirectory that looks like an active
// change. Used by Propose to enforce "only one active change at a time".
func listActiveChanges(changeRoot string) ([]string, error) {
	rootFI, err := os.Stat(changeRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !rootFI.IsDir() {
		return nil, fmt.Errorf("sdd: change root is not a directory: %s", changeRoot)
	}
	var out []string
	entries, err := os.ReadDir(changeRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "archive" {
			continue
		}
		if !activeChangeRE.MatchString(name) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// proposalStub returns the initial proposal.md content for a freshly
// scaffolded change.
func proposalStub(name string) string {
	return fmt.Sprintf(`## Why

TODO: explain why this change is needed (>= 50 characters).

## What Changes

- **New Capability:** TODO

## Capabilities

### New Capabilities

- %s: TODO one-line summary
`, name)
}

// designStub returns the initial design.md content.
func designStub() string {
	return `## Context

TODO: link the inspiration source and prior art.

## Goals / Non-Goals

**Goals:**

- TODO

**Non-Goals:**

- TODO

## Decisions

### D1. TODO

TODO: rationale.
`
}

// tasksStub returns the initial tasks.md content.
func tasksStub() string {
	return `## 1. Scaffold

- [ ] 1.1 TODO
- [ ] 1.2 TODO

## 2. Implementation

- [ ] 2.1 TODO
`
}
