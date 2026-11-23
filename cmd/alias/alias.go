// Package alias is nimble alias: directory aliases — <name> alone runs cd <dir>.
package alias

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/keyval"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdAlias() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias <command>",
		Short: "Directory aliases: <name> alone goes to a folder (alias add: the one you're in)",
		Long: heredoc.Doc(`
			A directory alias is a name that goes to a folder: nimble alias add 1a1 in a folder,
			then 1a1 alone runs cd there. The change works in the shell you typed it in at
			once, and in every new one. Aliases are yours: they are kept in
			personal/aliases.yaml, folders under your home as ~/…, so nimble push takes them
			to your other workstations.

			Every host in the ssh config has an alias too: grizzly alone runs ssh grizzly
			(nimble alias list shows them).
		`),
		Example: heredoc.Doc(`
			cd ~/workspace/classrooms/2026-1aWI-SWP && nimble alias add 1a1
			nimble alias add          # asks for the name
			nimble alias remove 1a1
			nimble alias remove       # pick it from a list
		`),
	}
	cmd.AddCommand(newList(), newAdd(), newRemove())
	cmd.RunE = keyval.Actions(cmd, "Aliases", []keyval.Action{
		{Label: "add one for this folder    (alias add)", Sub: "add"},
		{Label: "remove one                 (alias remove)", Sub: "remove"},
		{Label: "list them                  (alias list)", Sub: "list"},
	})
	return cmd
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "Show the directory aliases and the hosts' aliases", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := config.DirAliases()
			if err != nil {
				return err
			}
			names := sorted(m)
			if len(names) == 0 {
				fmt.Println("  no directory aliases yet — in a folder: nimble alias add <name>")
			}
			w := 4
			for _, n := range names {
				w = max(w, len(n))
			}
			for _, n := range names {
				note := ""
				if fi, err := os.Stat(config.ExpandDir(m[n])); err != nil || !fi.IsDir() {
					note = nimble.Yellow("   (no such folder on this machine)")
				}
				fmt.Printf("  %-*s  %s%s\n", w, n, m[n], note)
			}
			var hosts []string
			for _, l := range strings.Split(nimble.ReadTrim(nimble.CacheDir("ssh_config")), "\n") {
				if h, ok := strings.CutPrefix(l, "Host "); ok && !strings.ContainsAny(h, "*?! ") {
					if _, dir := m[h]; dir {
						h += " (a directory alias)"
					} else if p, err := exec.LookPath(h); err == nil {
						h += " (taken: " + p + ")"
					}
					hosts = append(hosts, h)
				}
			}
			if len(hosts) > 0 {
				fmt.Println("\n  hosts (<host> alone runs ssh <host>): " + strings.Join(hosts, ", "))
			}
			return nil
		},
	}
}

func newAdd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name]",
		Short: "Add an alias for the folder you're in (asks for the name if it's missing)",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp // a new name
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			dir := config.HomeDir(wd)
			m, err := config.DirAliases()
			if err != nil {
				return err
			}
			valid := func(name string) error { return checkName(name, dir) }
			var name string
			if len(args) > 0 {
				name = args[0]
				if err := valid(name); err != nil {
					return err
				}
			} else {
				if !nimble.Interactive() {
					return errors.New("missing alias name: nimble alias add <name>")
				}
				if name, err = nimble.Input("Alias for "+dir+" — name:", suggest(wd, m), valid); err != nil {
					return err
				}
			}
			switch old, ok := m[name]; {
			case ok && old == dir:
				nimble.Say(name + " goes to " + dir + " already")
			case ok:
				if nimble.Interactive() {
					yes, err := nimble.ConfirmOrCancel(name+" goes to "+old+" — make it go to "+dir+"?", true)
					if err != nil {
						return err
					}
					if !yes {
						return nimble.ErrCancelled
					}
				}
				fallthrough
			default:
				m[name] = dir
				if err := save(m); err != nil {
					return err
				}
				nimble.Say("alias " + name + " → cd " + dir)
				warnOthers(name)
			}
			return takeEffect(nil)
		},
	}
}

func newRemove() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [name…]",
		Short: "Remove directory aliases (a list to pick from if none is named)",
		Args:  cobra.ArbitraryArgs,
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			m, _ := config.DirAliases()
			given := map[string]bool{}
			for _, a := range args {
				given[a] = true
			}
			var out []string
			for _, n := range sorted(m) {
				if !given[n] {
					out = append(out, n+"\t"+m[n])
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := config.DirAliases()
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				if len(m) == 0 {
					nimble.Say("no directory aliases")
					return nil
				}
				if !nimble.Interactive() {
					return errors.New("missing alias name: nimble alias remove <name>")
				}
				labels, byLabel := []string{}, map[string]string{}
				w := 0
				for _, n := range sorted(m) {
					w = max(w, len(n))
				}
				for _, n := range sorted(m) {
					l := fmt.Sprintf("%-*s  %s", w, n, m[n])
					labels, byLabel[l] = append(labels, l), n
				}
				pick, err := nimble.Select("Remove which alias? (ESC or Ctrl+C to cancel)", labels, "")
				if errors.Is(err, nimble.ErrCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
				names = []string{byLabel[pick]}
			}
			for _, n := range names {
				if _, ok := m[n]; !ok {
					return fmt.Errorf("no alias %s (nimble alias list)", n)
				}
			}
			for _, n := range names {
				nimble.Say("removed alias " + n + " (cd " + m[n] + ")")
				delete(m, n)
			}
			if err := save(m); err != nil {
				return err
			}
			return takeEffect(names)
		},
	}
}

// reserved are names the shell itself needs: an alias would take them over.
var reserved = map[string]bool{"nimble": true, "alias": true, "unalias": true, "cd": true, "command": true, "builtin": true,
	"source": true, "export": true, "exit": true, "exec": true, "eval": true, "set": true, "unset": true, "local": true,
	"return": true, "if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true, "while": true, "do": true,
	"done": true, "case": true, "esac": true, "function": true, "time": true, "test": true, "type": true, "echo": true,
	"read": true, "pwd": true, "jobs": true, "fg": true, "bg": true, "kill": true, "wait": true, "trap": true, "hash": true}

// checkName: a name a shell takes for an alias, and not one a command or the shell has.
func checkName(name, dir string) error {
	if err := config.CheckAlias(name, dir); err != nil {
		return err
	}
	if reserved[name] {
		return fmt.Errorf("%s is the shell's own — pick another name", name)
	}
	if p, err := exec.LookPath(name); err == nil {
		return fmt.Errorf("%s is a command (%s) — pick another name", name, p)
	}
	return nil
}

// suggest is the folder's name, when it makes a free alias name.
func suggest(wd string, m map[string]string) string {
	s := strings.ToLower(filepath.Base(wd))
	if _, taken := m[s]; taken || checkName(s, "x") != nil {
		return ""
	}
	return s
}

// save writes personal/aliases.yaml and the alias file. Like other hand edits in the
// config repo, nimble push commits and publishes it.
func save(m map[string]string) error {
	if errs := config.Validate(setup.KnownTools()); len(errs) > 0 { // never leave a file push would reject
		return errs[0]
	}
	if err := config.SetDirAliases(m); err != nil {
		return err
	}
	return setup.WriteAliases()
}

// takeEffect makes the change work in the calling shell: through the nimble function (the
// alias file defines it) the shell drops the removed aliases and loads the file again.
// Without it, it says how the shell gets it.
func takeEffect(removed []string) error {
	next := os.Getenv(setup.NextEnv)
	if next == "" || !filepath.IsAbs(next) || !strings.HasPrefix(filepath.Base(next), "nimble-next.") {
		if strings.Contains(nimble.ReadTrim(filepath.Join(nimble.Home(), ".zshrc"))+nimble.ReadTrim(filepath.Join(nimble.Home(), ".bashrc")), setup.AliasesFile) {
			nimble.Step("in this shell from its next start (or now: exec $SHELL)")
		} else {
			nimble.Step("the shells don't load nimble's aliases yet: nimble apply once, then a new shell")
		}
		nimble.Step("nimble push takes it to your other workstations")
		return nil
	}
	var b strings.Builder
	for _, n := range removed {
		b.WriteString("unalias " + n + " 2>/dev/null\n")
	}
	b.WriteString(`. "` + nimble.CacheDir(setup.AliasesFile) + `"` + "\n")
	if err := os.WriteFile(next, []byte(b.String()), 0o600); err != nil {
		return err
	}
	nimble.Step("works in this shell now; nimble push takes it to your other workstations")
	return nil
}

// warnOthers says where else the name is an alias: nimble's loads last and wins.
func warnOthers(name string) {
	for _, f := range []string{"shell/common.sh", "personal/shell/common.sh", "local.sh"} {
		for _, l := range strings.Split(nimble.ReadTrim(nimble.RepoFile(f)), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "alias "+name+"=") {
				nimble.Warn(name + " is an alias in ~/.nimble/" + f + " too — this one wins (it loads last); that line can go")
			}
		}
	}
}

func sorted(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
