package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/update"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdTool() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool <command>",
		Short: "The tool catalog; select a machine's tools and shell, pin the languages' versions",
		Long: heredoc.Doc(`
			The catalog lists what a template can install (Homebrew on macOS, apt or official
			releases on Debian). tool select picks a machine's tools from it: checked ones are
			installed now and recorded in its host file (tools:), so they come back on every
			update; unchecked ones of the host file are uninstalled (after a warning and a yes).
			Without --host it acts on this machine; with --host (from the workstation) it
			records the change, then sends the config to that host and applies it there.
			tool shell picks the login shell (zsh, bash or the template's) the same way.

			The languages (node, go, rust, python) run at the version in toolchain/pins.yaml,
			the same on every machine: tool list shows them with their updates, tool use picks
			one, tool update writes the updates. use and update only edit pins.yaml; nimble
			push shares it.
		`),
	}
	sel := newSelect()
	sel.Flags().StringVar(&onHost, "host", "", "a host instead of this machine (from the workstation)")
	sel.RegisterFlagCompletionFunc("host", hosts.CompleteHostNames)
	cmd.AddCommand(newList(), sel, newShell(), newUse(), newUpdate(), newUninstall())
	return cmd
}

// uninstall takes tools off this machine where nimble can (Homebrew, apt, npm, its own
// release binaries); the others it only stops managing.
func uninstall(names []string) error {
	for _, a := range names {
		t, ok := setup.FindTool(a)
		switch {
		case !ok:
			return fmt.Errorf("unknown tool %q", a)
		case t.Remove == nil:
			nimble.Say("note: " + a + ": " + setup.ErrNoRemove.Error())
		case t.Installed():
			if err := t.Remove(); err != nil {
				return err
			}
			nimble.Say("removed " + a)
		}
	}
	return nil
}

// newUninstall is the plumbing tool select --host runs on the host: it only uninstalls
// (the host file was changed on the workstation).
func newUninstall() *cobra.Command {
	return &cobra.Command{
		Use: "uninstall <tool>…", Hidden: true, Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return uninstall(args) },
	}
}

var onHost string

// target is the host file to change: --host, else this machine's. remote is true when
// that host is another machine (the change is applied there with nimble update).
func target() (h config.Host, remote bool, err error) {
	if me, ok := hosts.Self(""); onHost == "" || onHost == hosts.MachineName() || (ok && me.Name == onHost) {
		h, err = self()
		return h, false, err
	}
	if err := nimble.EditableHost(onHost); err != nil { // on a host: only its own
		return h, false, err
	}
	h, ok := config.FindHost(onHost)
	if !ok {
		return h, false, errors.New("no host " + onHost + " (nimble host list)")
	}
	if !h.Managed() {
		return h, false, errors.New(onHost + " isn't managed by nimble (mode " + h.Mode + ")")
	}
	return h, true, nil
}

func completeTools(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	os := config.OS()
	if h, _ := cmd.Flags().GetString("host"); h != "" && h != hosts.MachineName() {
		os = "debian" // --host: a managed host
	}
	var out []string
	for _, t := range setup.CatalogFor(os) {
		out = append(out, t.Name+"\t"+t.Desc)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// targetOS is the system of the machine a tool command configures: this one, or a managed
// host (Debian) with --host.
func targetOS(remote bool) string {
	if remote {
		return "debian"
	}
	return config.OS()
}

func self() (config.Host, error) {
	h, ok := hosts.Self("")
	if !ok {
		return h, errors.New("this machine has no host file (hosts/" + hosts.MachineName() + ".yaml) — create one: nimble host create")
	}
	return h, nil
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "Show the catalog, what this machine has, and updates of the pinned versions", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			var hp *config.Host
			if h, ok := hosts.Self(""); ok {
				hp = &h
			}
			r, err := config.Resolve(c, hp)
			if err != nil {
				return err
			}
			wanted := map[string]bool{}
			for _, t := range r.Tools {
				wanted[t] = true
			}
			for _, t := range setup.CatalogFor(config.OS()) {
				state := "  "
				switch {
				case wanted[t.Name] && t.Installed():
					state = nimble.Green("✓ ")
				case wanted[t.Name]:
					state = nimble.Red("✗ ")
				case t.Installed():
					state = nimble.Yellow("• ")
				}
				fmt.Printf("%s%-10s %s\n", state, t.Name, t.Desc)
			}
			fmt.Println("\n✓ wanted and installed   ✗ wanted, missing   • installed, not managed by nimble")
			fmt.Println("\n" + nimble.Bold("Pinned versions") + " (toolchain/pins.yaml) — nimble tool use picks one")
			return check(false, false, nil, false)
		},
	}
}

func newSelect() *cobra.Command {
	return &cobra.Command{
		Use:   "select",
		Short: "Select a machine's tools: check to install, uncheck to uninstall (this machine, or --host)",
		Long: heredoc.Doc(`
			Pick the machine's tools from the catalog for its system, with what it has checked
			and marked where it comes from. Checked tools are installed now and recorded in
			its host file (tools:), so they come back on every update. Unchecked tools of the
			host file are taken out and uninstalled — nimble first warns what gets uninstalled
			(and what it only stops managing) and asks. The template's tools stay: they change
			with the template. Without a terminal: nimble host set <host> tools=a,b,c. On a
			host it changes the host's own file; the workstation's next nimble update <host>
			takes the change into the config repo.
		`),
		Example: "  nimble tool select                  # this machine\n  nimble tool select --host devbox    # devbox, from the workstation",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, remote, err := target()
			if err != nil {
				return err
			}
			if remote && nimble.Interactive() {
				// first take in what was changed on the host since its last snapshot
				if err := update.ReadBack(h.Name); err != nil {
					return err
				}
				h, _ = config.FindHost(h.Name)
			}
			return selectTools(h, remote)
		},
	}
}

// selectTools is tool select's picker: the catalog for the machine's system with what it
// has checked and marked. Checked tools are added, unchecked tools of the host file removed
// (change warns and asks first); the template's tools stay.
func selectTools(h config.Host, remote bool) error {
	if !nimble.Interactive() {
		return errors.New("tool select needs a terminal — without one: nimble host set " + h.Name + " tools=a,b,c")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	r, err := config.Merge(c, &h)
	if err != nil {
		return err
	}
	has := toolSources(r, h)
	catalog := setup.CatalogFor(targetOS(remote))
	labels, checked, names := addChoices(catalog, has)
	chosen, err := nimble.Checklist("Tools for "+h.Name+" — check to add, uncheck to remove (space toggles, enter applies):", labels, checked)
	if err != nil {
		return err
	}
	add, drop, kept := picked(catalog, chosen, names, has)
	for _, t := range kept {
		nimble.Say("note: " + t + " stays — it comes from " + has[t] + " (change the template for that)")
	}
	if len(add) == 0 && len(drop) == 0 {
		nimble.Say("nothing changed")
		return nil
	}
	return change(h, remote, add, drop)
}

// change adds and removes tools on h in one go. Only the host file's own tools can be
// removed (the template's change with the template); nimble warns what that uninstalls and,
// on a terminal, asks first. Then on this machine the tools are uninstalled and installed,
// the host file is written and committed once; with --host that host is updated once, and
// what was removed is uninstalled there.
func change(h config.Host, remote bool, add, drop []string) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	for _, a := range append(append([]string{}, add...), drop...) {
		if _, ok := setup.FindTool(a); !ok {
			return fmt.Errorf("unknown tool %q (nimble tool list)", a)
		}
	}
	// the host's template, read directly: it may be for another system than this one
	name := h.Template
	if name == "" {
		name = c.DefaultTemplate
	}
	fromTemplate := map[string]bool{}
	if name != "" {
		t, err := config.FindTemplate(name)
		if err != nil {
			return err
		}
		for _, x := range t.Tools {
			fromTemplate[x] = true
		}
	}
	own := map[string]bool{}
	for _, x := range h.Tools {
		own[x] = true
	}
	for _, d := range drop {
		switch {
		case fromTemplate[d]:
			return fmt.Errorf("%s comes from the template %s — change the template, or give this host another one", d, name)
		case !own[d]:
			return fmt.Errorf("%s isn't in %s's host file", d, h.Name)
		}
	}

	if len(drop) > 0 {
		where := "this machine"
		if remote {
			where = h.Name
		}
		nimble.Warn("removing from " + where + ":")
		for _, d := range drop {
			t, _ := setup.FindTool(d)
			what := "uninstalled"
			if t.Remove == nil {
				what = "stays installed — nimble only stops managing it"
			}
			fmt.Printf("  %-10s %s\n", d, what)
		}
		if nimble.Interactive() {
			yes, err := nimble.ConfirmOrCancel("Remove "+join(drop)+"?", false)
			if err != nil {
				return err
			}
			if !yes {
				nimble.Say("nothing removed")
				if drop = nil; len(add) == 0 {
					return nil
				}
			}
		}
	}

	if !remote {
		if err := uninstall(drop); err != nil {
			return err
		}
		if len(add) > 0 {
			if err := setup.InstallTools(add); err != nil {
				return err
			}
		}
	}
	gone := map[string]bool{}
	for _, d := range drop {
		gone[d] = true
	}
	var tools []string
	for _, x := range h.Tools {
		if !gone[x] {
			tools = append(tools, x)
		}
	}
	for _, a := range add {
		if !own[a] {
			tools, own[a] = append(tools, a), true
		}
	}
	if err := config.SetHost(h.Name, "tools", join(tools)); err != nil {
		return err
	}
	var what []string
	if len(add) > 0 {
		what = append(what, "add tools "+join(add))
	}
	if len(drop) > 0 {
		what = append(what, "remove tools "+join(drop))
	}
	if err := nimble.Record("hosts: "+h.Name+" "+strings.Join(what, ", "), "hosts"); err != nil || !remote {
		return err
	}
	if err := update.ToHosts([]string{h.Name}); err != nil { // installs the new ones there
		return err
	}
	if len(drop) == 0 {
		return nil
	}
	// the host's snapshot no longer lists them: uninstall them there
	return hosts.Nimble(h.Name, hosts.Opts{Agent: true}, "tool uninstall "+strings.Join(drop, " "))
}

// toolSources maps the tools a machine already has to where they come from.
func toolSources(r config.Resolved, h config.Host) map[string]string {
	has := map[string]string{}
	for _, t := range r.TemplateTools {
		has[t] = "template " + r.Template
	}
	for _, t := range h.Tools {
		if has[t] == "" {
			has[t] = "host file"
		}
	}
	return has
}

// addChoices is tool select's list: every catalog tool with its short label, the ones the
// machine has checked and marked (template) or (host file). names maps a label back.
func addChoices(catalog []setup.Tool, has map[string]string) (labels, checked []string, names map[string]string) {
	names = map[string]string{}
	for _, t := range catalog {
		short := t.Short
		if short == "" {
			short = t.Desc
		}
		l := fmt.Sprintf("%-9s %s", t.Name, short)
		if from := has[t.Name]; from != "" {
			if strings.HasPrefix(from, "template") {
				from = "template" // a machine has one template; the note after the picker names it
			}
			l += " (" + from + ")"
			checked = append(checked, l)
		}
		labels = append(labels, l)
		names[l] = t.Name
	}
	return labels, checked, names
}

// picked splits the answer, in catalog order: the newly checked tools (add), the unchecked
// tools of the host file (drop), and the unchecked tools of the template (kept: they stay).
func picked(catalog []setup.Tool, chosen []string, names, has map[string]string) (add, drop, kept []string) {
	on := map[string]bool{}
	for _, l := range chosen {
		on[names[l]] = true
	}
	for _, t := range catalog {
		switch from := has[t.Name]; {
		case on[t.Name] && from == "":
			add = append(add, t.Name)
		case !on[t.Name] && from == "host file":
			drop = append(drop, t.Name)
		case !on[t.Name] && from != "":
			kept = append(kept, t.Name)
		}
	}
	return add, drop, kept
}

func join(list []string) string {
	s := ""
	for i, x := range list {
		if i > 0 {
			s += ","
		}
		s += x
	}
	return s
}
