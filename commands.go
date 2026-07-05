// commands.go — the four canonical SDD slash commands.
//
// Each command is a thin wrapper around the corresponding lifecycle.go
// function; the Command interface (tau.Command alias for slash.Command)
// just carries Name, ShortHelp, and Execute. The wrappers exist to
// adapt the lifecycle function signature to the Command interface and
// to surface per-command metadata for /help.
//
// Per the archived open-slash-command-surface spec, the Execute
// signature is `Execute(ctx, args, session tau.CommandSession) (string,
// error)` — NOT the older `*agent.AgentSession` signature that the
// task spec text still references. This file targets the
// post-open-slash-command-surface SDK; the change's task descriptions
// at sections 5.1-5.5 need an asterisk-free update at archive time.
package sdd

import (
	"context"
	"errors"
	"strings"

	tau "github.com/coevin/tau/pkg/tau"
)

// ProposeCommand is the /propose slash command. It scaffolds a new
// change directory under openspec/changes/.
type ProposeCommand struct {
	changeRoot string
}

// NewProposeCommand returns a /propose command bound to changeRoot.
// Pass "" to resolve via defaultChangeRoot() at Execute time.
func NewProposeCommand(changeRoot string) *ProposeCommand {
	return &ProposeCommand{changeRoot: changeRoot}
}

// Name returns the canonical invocation including the leading slash.
func (c *ProposeCommand) Name() string { return "/propose" }

// ShortHelp returns a one-line description for /help.
func (c *ProposeCommand) ShortHelp() string {
	return "Scaffold a new openspec change directory"
}

// Execute scaffolds the four artifacts for the change named in args.
// args is the trimmed text after the command name; it MUST be a single
// kebab-case token.
func (c *ProposeCommand) Execute(ctx context.Context, args string, session tau.CommandSession) (string, error) {
	name := strings.TrimSpace(args)
	if name == "" {
		return "", errors.New("sdd: /propose requires a change name")
	}
	return Propose(ctx, c.changeRoot, name)
}

// ExploreCommand is the /explore slash command. It enters a read-only
// thinking mode and writes no artifacts.
type ExploreCommand struct{}

// NewExploreCommand returns a /explore command.
func NewExploreCommand() *ExploreCommand { return &ExploreCommand{} }

// Name returns the canonical invocation.
func (c *ExploreCommand) Name() string { return "/explore" }

// ShortHelp returns a one-line description.
func (c *ExploreCommand) ShortHelp() string {
	return "Enter a read-only thinking mode for an idea"
}

// Execute runs Explore with the wired session.
func (c *ExploreCommand) Execute(ctx context.Context, args string, session tau.CommandSession) (string, error) {
	return Explore(ctx, session, args)
}

// ApplyCommand is the /apply slash command. It walks tasks.md and
// reports per-section progress.
type ApplyCommand struct {
	changeRoot string
	changeName string
}

// NewApplyCommand returns a /apply command for the named change.
// changeName may be empty; when it is, Execute derives the active
// change from the sole entry under openspec/changes/.
func NewApplyCommand(changeRoot, changeName string) *ApplyCommand {
	return &ApplyCommand{changeRoot: changeRoot, changeName: changeName}
}

// Name returns the canonical invocation.
func (c *ApplyCommand) Name() string { return "/apply" }

// ShortHelp returns a one-line description.
func (c *ApplyCommand) ShortHelp() string {
	return "Walk the active change's tasks top-to-bottom"
}

// Execute runs Apply against the configured change. When no change
// name is configured, Execute reads the sole active change directory.
func (c *ApplyCommand) Execute(ctx context.Context, args string, session tau.CommandSession) (string, error) {
	name := strings.TrimSpace(args)
	if name == "" {
		name = c.changeName
	}
	if name == "" {
		// Fall back to the sole active change under changeRoot.
		root := c.changeRoot
		if root == "" {
			abs, err := defaultChangeRoot()
			if err != nil {
				return "", err
			}
			root = abs
		}
		active, err := listActiveChanges(root)
		if err != nil {
			return "", err
		}
		if len(active) == 0 {
			return "", errors.New("sdd: no active change under openspec/changes/")
		}
		if len(active) > 1 {
			return "", errors.New("sdd: multiple active changes; specify which one")
		}
		name = active[0]
	}
	return Apply(ctx, session, c.changeRoot, name)
}

// ArchiveCommand is the /archive slash command. It folds specs into
// canonical and moves the change directory to archive/<date>-<name>/.
type ArchiveCommand struct {
	changeRoot string
}

// NewArchiveCommand returns a /archive command bound to changeRoot.
func NewArchiveCommand(changeRoot string) *ArchiveCommand {
	return &ArchiveCommand{changeRoot: changeRoot}
}

// Name returns the canonical invocation.
func (c *ArchiveCommand) Name() string { return "/archive" }

// ShortHelp returns a one-line description.
func (c *ArchiveCommand) ShortHelp() string {
	return "Archive a completed change after folding its specs"
}

// Execute runs Archive against the named change.
func (c *ArchiveCommand) Execute(ctx context.Context, args string, session tau.CommandSession) (string, error) {
	name := strings.TrimSpace(args)
	if name == "" {
		return "", errors.New("sdd: /archive requires a change name")
	}
	return Archive(ctx, c.changeRoot, name)
}

// Compile-time assertions that each command satisfies tau.Command
// (alias for slash.Command). If a future change to the Command
// interface adds a method, these lines fail the build.
var (
	_ tau.Command = (*ProposeCommand)(nil)
	_ tau.Command = (*ExploreCommand)(nil)
	_ tau.Command = (*ApplyCommand)(nil)
	_ tau.Command = (*ArchiveCommand)(nil)
)
