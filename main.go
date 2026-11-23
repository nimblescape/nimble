// Command nimble sets up a developer workstation (macOS or Debian) and the Debian hosts it
// reaches over SSH from one config repo (~/.nimble), and keeps them identical: shell,
// linked config, pinned languages and tools, SSH keys, host hardening and services.
//
// docs/how-it-works.md describes what each part does.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nimblescape/nimble/cmd/push"
	"github.com/nimblescape/nimble/cmd/root"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	nimble.SetupEnv()
	nimble.Version = version

	ran, err := root.NewRootCmd(version).ExecuteC()
	hosts.Cleanup()
	status := 0
	switch {
	case errors.Is(err, nimble.ErrProblems):
		status = 1 // already reported
	case errors.Is(err, nimble.ErrCancelled):
		fmt.Println() // Esc or Ctrl+C in a prompt: a graceful exit (0), not an error
	case err != nil:
		fmt.Fprintln(os.Stderr, nimble.Red("error:"), err)
		status = 1
	}
	if remind(ran) {
		push.WarnUnpublished() // last, on stderr: $(nimble path) stays clean
	}
	os.Exit(status)
}

// remind reports whether to warn about unpublished changes after a command: after every
// one, except shell completion (the shell parses its output), help, and nimble changes
// (it ends with the same warning).
func remind(c *cobra.Command) bool {
	if c == nil {
		return false
	}
	switch c.Name() {
	case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd, "completion", "help", "changes":
		return false
	}
	if p := c.Parent(); p != nil && p.Name() == "completion" { // nimble completion zsh, …
		return false
	}
	if f := c.Flags().Lookup("help"); f != nil && f.Changed { // nimble … --help
		return false
	}
	return true
}
