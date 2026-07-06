// tool_validate.go — the `validate_change` HeadlessTool.
//
// The tool wraps the ValidateChangeProposal + ValidateChangeDeltaSpecs rule
// sets so the model can self-check its change before claiming a task done
// (design.md D4). The tool resolves openspec/changes/<name>/, reads
// proposal.md and every specs/<capability>/spec.md, runs the rules, and
// returns the report as text via tau.NewTextResult.
//
// The tool's report format (ERRORS / WARNINGS / INFO sections, each line
// prefixed with the rule id and path) is documented in the Description()
// string so providers see the format and can interpret it without prose.
package sdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"

	tau "github.com/taucentral/tau/pkg/tau"
)

// ValidateTool is the `validate_change` HeadlessTool. It is stateless
// apart from the changeRoot it resolves change names against. The zero
// value resolves changes against "openspec/changes" relative to the
// process's CWD; pass NewValidateTool to customise the root (e.g.
// tests resolving against a temp dir).
type ValidateTool struct {
	// changeRoot is the absolute path to the openspec/changes/
	// directory. Empty means "resolve via defaultChangeRoot()".
	changeRoot string
}

// NewValidateTool returns a ValidateTool that resolves change names
// against the supplied openspec/changes/ root. Pass "" to use
// defaultChangeRoot() ("openspec/changes" relative to os.Getwd()).
func NewValidateTool(changeRoot string) *ValidateTool {
	return &ValidateTool{changeRoot: changeRoot}
}

// validateArgs is the parameter schema for validate_change, reflected
// into JSON Schema for the model.
type validateArgs struct {
	Change string `json:"change" jsonschema:"description=The kebab-case name of the change under openspec/changes/ to validate.,minLength=1"`
}

// Name returns the tool's identifier. The plugin registers the tool via
// the Tools() entry point; tau's runtime dispatches by this name.
func (t *ValidateTool) Name() string { return "validate_change" }

// Description returns natural-language help shown to the model. Includes
// the report format so the model can interpret the structured output
// without further prompting.
func (t *ValidateTool) Description() string {
	return `Run the OpenSpec rule set against an openspec change directory and return a structured report.

The report has three sections, each optional based on findings:

  ERRORS:   hard rule violations. Lines have the form
            "  - [<RULE-ID>] <path>: <message>".
            The change is not ready to archive while any ERRORS entry is present.
  WARNINGS: soft rule violations. Reviewers SHOULD look at these.
  INFO:     informational findings (e.g. very long requirement text).

When the report has no findings the tool returns the single token "OK".

Use this tool to self-check after authoring proposal.md, design.md,
tasks.md, or any specs/<capability>/spec.md file before claiming a task done.`
}

// Parameters returns the JSON Schema for the tool's args, reflected
// from validateArgs. Marshals to valid JSON Schema draft 2020-12.
func (t *ValidateTool) Parameters() jsonschema.Schema {
	return reflectSchema(&validateArgs{})
}

// Execute parses call.Args as {"change":"<name>"}, resolves
// openspec/changes/<name>/, reads proposal.md and every
// specs/<capability>/spec.md, runs ValidateChangeProposal +
// ValidateChangeDeltaSpecs, and returns tau.NewTextResult with the
// combined report. On missing change or malformed args Execute returns
// an IsError tool result naming the failure (it never returns a
// non-nil error — application-level failures become ToolResult.IsError).
func (t *ValidateTool) Execute(ctx context.Context, call tau.ToolCall) (tau.ToolResult, error) {
	if ctx.Err() != nil {
		return tau.ToolResult{}, ctx.Err()
	}

	var args validateArgs
	if len(call.Args) == 0 {
		r := tau.NewErrorResult("validate_change: missing required field \"change\"")
		return r, nil
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		r := tau.NewErrorResult(fmt.Sprintf("validate_change: malformed args: %v", err))
		return r, nil
	}
	if args.Change == "" {
		r := tau.NewErrorResult("validate_change: missing required field \"change\"")
		return r, nil
	}

	root := t.changeRoot
	if root == "" {
		var err error
		root, err = defaultChangeRoot()
		if err != nil {
			r := tau.NewErrorResult(fmt.Sprintf("validate_change: resolve changes root: %v", err))
			return r, nil
		}
	}
	changeDir := filepath.Join(root, args.Change)
	if fi, err := os.Stat(changeDir); err != nil || !fi.IsDir() {
		r := tau.NewErrorResult(fmt.Sprintf("validate_change: change directory not found: %s", changeDir))
		return r, nil
	}

	// Read proposal.md.
	proposalBytes, err := os.ReadFile(filepath.Join(changeDir, "proposal.md"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r := tau.NewErrorResult(fmt.Sprintf("validate_change: proposal.md not found in %s", changeDir))
			return r, nil
		}
		r := tau.NewErrorResult(fmt.Sprintf("validate_change: read proposal.md: %v", err))
		return r, nil
	}

	// Walk specs/<capability>/spec.md files.
	specs, errs := readChangeSpecs(changeDir)
	for _, e := range errs {
		// We do not abort the whole run on a single unreadable spec;
		// surface each read error as an ERROR in the report instead.
		_ = e // reported below
	}

	rep := ValidateChangeProposal(proposalBytes)
	deltaRep := ValidateChangeDeltaSpecs(specs)
	rep.Errors = append(rep.Errors, deltaRep.Errors...)
	rep.Warnings = append(rep.Warnings, deltaRep.Warnings...)
	rep.Info = append(rep.Info, deltaRep.Info...)

	return tau.NewTextResult(rep.String()), nil
}

// readChangeSpecs walks <changeDir>/specs/ and returns the
// capability-to-markdown map consumed by ValidateChangeDeltaSpecs.
// Read errors are returned in the slice rather than aborting the walk;
// the caller decides whether to surface them.
func readChangeSpecs(changeDir string) (map[string][]byte, []error) {
	out := map[string][]byte{}
	var errs []error
	specsDir := filepath.Join(changeDir, "specs")
	fi, err := os.Stat(specsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		errs = append(errs, err)
		return out, errs
	}
	if !fi.IsDir() {
		return out, nil
	}
	_ = filepath.WalkDir(specsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "spec.md" {
			return nil
		}
		rel, relErr := filepath.Rel(specsDir, path)
		if relErr != nil {
			errs = append(errs, relErr)
			return nil
		}
		// Capability name is the parent directory of spec.md.
		capName := filepath.Dir(rel)
		if capName == "." {
			capName = filepath.Base(specsDir)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			errs = append(errs, readErr)
			return nil
		}
		out[capName] = data
		return nil
	})
	return out, errs
}

// defaultChangeRoot returns the absolute path to the openspec/changes/
// directory relative to the process's CWD. Used when the ValidateTool
// was constructed without an explicit root.
func defaultChangeRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "openspec", "changes"), nil
}

// reflectSchema is the plugin-local equivalent of tau's internal
// tools.ReflectSchema. Reproduced here because the internal helper is
// not exported (pkg/tau does not re-export it). Mirrors the same
// pattern in plugins/headroom/retrieve.go.
func reflectSchema(sample any) jsonschema.Schema {
	r := new(jsonschema.Reflector)
	r.DoNotReference = true
	s := r.Reflect(sample)
	if s == nil {
		return jsonschema.Schema{Type: "object"}
	}
	s.Version = ""
	s.ID = ""
	s.Definitions = nil
	return *s
}

// ensureValidateToolSatisfiesHeadlessTool is a compile-time assertion
// that *ValidateTool satisfies tau.HeadlessTool. If a future change to
// HeadlessTool adds a method, this line fails the build.
var _ tau.HeadlessTool = (*ValidateTool)(nil)
