package host

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/initialize"
	"github.com/nimblescape/nimble/cmd/keyval"
	"github.com/nimblescape/nimble/cmd/repo"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdHost() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host <command>",
		Short: "Manage the hosts: one YAML file per machine in hosts/",
		Long: heredoc.Doc(`
			hosts/ holds one YAML file per machine, workstations included: address, admin
			user, domain, mode (managed, ssh, workstation), tools, login shell and services.
			A template is copied in (host create, host set <name> template=…), not referenced.
			The workstation's ssh config is generated from it (jump: reached through another
			host); it stays there. A managed host with reach gets its own, for those hosts.
		`),
	}
	cmd.AddCommand(newCreate(), newSet(), newEdit(), newList(), newShow(), newRemove())
	cmd.RunE = keyval.Actions(cmd, "Hosts", []keyval.Action{ // nimble host alone: a menu
		{Label: "change a host's settings   (host set)", Sub: "set"},
		{Label: "add a host                 (host create)", Sub: "create"},
		{Label: "show a host's settings     (host show)", Sub: "show"},
		{Label: "list the hosts             (host list)", Sub: "list"},
		{Label: "edit a host file           (host edit)", Sub: "edit"},
		{Label: "remove a host              (host remove)", Sub: "remove"},
	})
	return cmd
}

// regenerate rewrites the ssh config for the hosts on a workstation (a Mac, or a Debian
// machine whose host file says mode: workstation).
func regenerate() {
	self, ok := hosts.Self("")
	if setup.Darwin() || (ok && self.Workstation()) {
		if c, err := config.Load(); err == nil {
			_ = setup.SSHConfig(c)
		}
	}
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// problems returns the validation errors that are about one host file.
func problems(name string) []error {
	var out []error
	for _, e := range config.Validate(setup.KnownTools()) {
		if strings.Contains(e.Error(), "hosts/"+name+".yaml") {
			out = append(out, e)
		}
	}
	return out
}

func newCreate() *cobra.Command {
	var h config.Host
	var from string
	var noInit bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Add a host (guided), then offer to set it up",
		Long: heredoc.Doc(`
			Write hosts/<name>.yaml — asking for whatever the flags don't give — regenerate
			the ssh config, commit, then offer nimble init <name>. The template's tools and
			shell are copied into the file: from then on they are the host's own, and a later
			change to the template doesn't change the host.
		`),
		Example: "  nimble host create devbox --address devbox.example.com",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			name, err := nimble.Arg(args, 0, "host name", nil)
			if err != nil {
				return err
			}
			if !nameRe.MatchString(name) || name == "all" {
				return fmt.Errorf("host name %q: lower-case letters, digits, '.', '-' and '_' (and not \"all\")", name)
			}
			h.Name = name
			if h.Mode == "" {
				h.Mode = nimble.Ask("Mode (managed, ssh, workstation)?", "managed")
			}
			switch h.Mode {
			case "managed", "ssh", "workstation":
			default:
				return fmt.Errorf("mode %q: managed, ssh or workstation", h.Mode)
			}
			// asked before the address: behind a jump host, it is the address seen from there
			if h.Jump == "" && h.Mode != "workstation" && !cmd.Flags().Changed("jump") {
				if others := connectableNames(""); len(others) > 0 {
					h.Jump = nimble.Ask("Reached through another host (jump: "+strings.Join(others, ", ")+"; empty = directly)?", "")
				}
			}
			if h.Address == "" && h.Mode != "workstation" {
				q := "Address (hostname or IP)?"
				if h.Jump != "" {
					q = "Address (hostname or IP, as seen from " + h.Jump + ")?"
				}
				if h.Address = nimble.Ask(q, ""); h.Address == "" {
					return errors.New("an address is needed")
				}
			}
			if h.User == "" && h.Mode != "workstation" {
				h.User = nimble.Ask("Admin user?", "debian")
			}
			if h.Domain == "" {
				def, _ := c.Domain("")
				h.Domain = nimble.Ask("Domain ("+strings.Join(c.DomainNames(), ", ")+")?", def.Name)
			}
			if _, err := c.Domain(h.Domain); err != nil {
				return err
			}
			// the template is copied in: its tools and shell become the host file's own
			if from == "" && h.Mode != "ssh" {
				ts, _ := config.Templates()
				var names []string
				for _, t := range ts {
					names = append(names, t.Name)
				}
				from = nimble.Ask("Template to copy ("+strings.Join(names, ", ")+"; none = no tools)?", c.DefaultTemplate)
			}
			if from != "" && from != "none" {
				t, err := config.TemplateFor(from, h)
				if err != nil {
					return err
				}
				h.Tools, h.Shell = t.Tools, t.Shell
			}
			if h.Mode == "managed" {
				h.Mode = "" // the default
			}
			if err := config.CreateHost(h); err != nil {
				return err
			}
			if errs := problems(h.Name); len(errs) > 0 { // never commit a file apply and push would reject
				_ = config.RemoveHost(h.Name)
				return errs[0]
			}
			nimble.Say("wrote hosts/" + h.Name + ".yaml")
			regenerate()
			if err := nimble.Record("hosts: add "+h.Name, "hosts"); err != nil {
				return err
			}
			if h.Managed() && !noInit && nimble.Confirm("Set "+h.Name+" up now (nimble init "+h.Name+")?", true) {
				return hosts.Each([]string{h.Name}, initialize.InitHost)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&h.Address, "address", "", "hostname or IP address")
	cmd.Flags().StringVar(&h.User, "user", "", "admin user (default debian)")
	cmd.Flags().StringVar(&h.Domain, "domain", "", "domain from nimble.yaml")
	cmd.Flags().StringVar(&from, "template", "", "template whose tools and shell are copied in (default: the default template)")
	cmd.Flags().StringVar(&h.Mode, "mode", "", "managed, ssh or workstation")
	cmd.Flags().StringVar(&h.Jump, "jump", "", "host it is reached through (ssh's ProxyJump); \"\" = directly")
	cmd.Flags().StringSliceVar(&h.Reach, "reach", nil, "hosts (or all) it gets ssh entries for, in its own ssh config")
	cmd.Flags().BoolVar(&noInit, "no-init", false, "don't offer to set the host up")
	// the flags with known values complete like host set's keys (a new host: no values yet)
	newHost := func() []keyval.Setting { return settings(config.Host{}) }
	for _, flag := range []string{"mode", "domain", "jump", "reach", "template"} {
		_ = cmd.RegisterFlagCompletionFunc(flag, keyval.CompleteFlag(flag, newHost))
	}
	return cmd
}

// connectableNames are the hosts ssh can connect to (not a workstation, with an address),
// but not the host except.
func connectableNames(except string) []string {
	hs, _ := config.Hosts()
	var out []string
	for _, h := range hs {
		if h.Name != except && !h.Workstation() && h.Address != "" {
			out = append(out, h.Name)
		}
	}
	return out
}

func newSet() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> key=value…",
		Short: "Change a host's settings (address, user, domain, mode, tools, shell, jump, reach; template= copies one in)",
		Long: heredoc.Doc(`
			Change settings of a host file: address, user, domain, mode, tools (comma list),
			shell, jump, reach (comma list); an empty value removes the key. template=<name>
			isn't kept as a setting: it copies that template's tools and shell over the host's
			(nimble warns what that drops from its tools, and asks on a terminal).

			jump=<host>: the host is reached through that one (ssh's ProxyJump) — from the
			workstation and from other hosts. Its address is resolved there, so it can be
			one of that network's (a VLAN address). Takes effect on the workstation at once.

			reach=<host,…|all>: the host gets its own ssh config with entries for these
			hosts (and the ones they are reached through), so ssh <host> works there too. It
			holds no keys: the workstation's key comes along in the forwarded agent, so Touch
			ID confirms each login. The workstation's own ssh config stays there.

			Without key=value, on a terminal, a menu lists the settings with their values:
			pick one, then its values from a list (or type it), and save. Tab completion
			offers the keys and, after key=, their values.
		`),
		Example: heredoc.Doc(`
			nimble host set devbox template=dev   # copy dev's tools and shell in
			nimble host set devbox tools=docker,gh
			nimble host set devbox shell=bash
			nimble host set grizzly jump=swan address=10.0.20.5
			nimble host set swan reach=grizzly    # ssh grizzly on swan
			nimble host set swan reach=           # no ssh config on swan
		`),
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeSet, // host, then key=value (tab completes keys and values)
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "host", hostNames())
			if err != nil {
				return err
			}
			if err := nimble.EditableHost(name); err != nil {
				return err
			}
			settings := args[min(1, len(args)):]
			if len(settings) == 0 { // the guided way: a menu of the settings
				if settings, err = menu(name); err != nil {
					return err
				}
				if len(settings) == 0 {
					nimble.Say("nothing changed")
					return nil
				}
			}
			return set(name, settings)
		},
	}
}

// set writes settings (key=value) into host name's file — all of them or, when one fails or
// the repo doesn't validate, none — regenerates the ssh config, commits and says what next.
func set(name string, settings []string) error {
	for _, kv := range settings {
		if k, v, _ := strings.Cut(kv, "="); k == "template" {
			if err := confirmCopy(name, v); err != nil {
				return err
			}
		}
	}
	before, err := os.ReadFile(config.HostFile(name))
	if err != nil {
		return fmt.Errorf("no host %s (nimble host list)", name)
	}
	restore := func() { _ = os.WriteFile(config.HostFile(name), before, 0o644) } // all settings, or none
	for _, kv := range settings {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			restore()
			return fmt.Errorf("%q: expected key=value", kv)
		}
		if err := config.SetHost(name, k, v); err != nil {
			restore()
			return err
		}
	}
	if errs := config.Validate(setup.KnownTools()); len(errs) > 0 {
		restore() // keep the file valid
		return errs[0]
	}
	nimble.Say("updated hosts/" + name + ".yaml")
	regenerate()
	if err := nimble.Record("hosts: "+name+" "+strings.Join(settings, " "), "hosts"); err != nil {
		return err
	}
	if nimble.IsSnapshot() && touchesSSH(settings) { // the other host files are on the workstation
		nimble.Step("the workstation renders the ssh config: nimble update " + name + " there")
		return nil
	}
	onlyJump := true // the workstation's ssh config has it already; nothing on the host
	for _, kv := range settings {
		if k, _, _ := strings.Cut(kv, "="); k != "jump" {
			onlyJump = false
		}
	}
	switch self, ok := hosts.Self(""); {
	case onlyJump:
	case ok && self.Name == name:
		nimble.Step("apply it: nimble update")
	default:
		nimble.Step("apply it: nimble update " + name)
	}
	// hosts that have an ssh entry for this one get the change with their own update
	if others := reachers(name); len(others) > 0 && touchesEntry(settings) {
		nimble.Step("and in the ssh config of " + strings.Join(others, ", ") + ": nimble update " + strings.Join(others, " "))
	}
	return nil
}

// touchesSSH: the settings change what the host's own ssh config holds.
func touchesSSH(settings []string) bool {
	for _, kv := range settings {
		if k, _, _ := strings.Cut(kv, "="); k == "reach" || k == "jump" {
			return true
		}
	}
	return false
}

// touchesEntry: the settings change the host's entry in other hosts' ssh config.
func touchesEntry(settings []string) bool {
	for _, kv := range settings {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "address", "user", "mode", "jump":
			return true
		}
	}
	return false
}

// reachers are the managed hosts whose own ssh config has an entry for host name.
func reachers(name string) []string {
	hs, _ := config.Hosts()
	var out []string
	for _, h := range hs {
		if h.Name != name && h.Managed() && strings.Contains(hosts.HostSSHConfig(h, hs), "Host "+name+"\n") {
			out = append(out, h.Name)
		}
	}
	return out
}

// confirmCopy says what copying template name over host's tools and shell drops, and on a
// terminal asks first; without one it goes ahead (the command line asked for it).
func confirmCopy(host, name string) error {
	h, ok := config.FindHost(host)
	if !ok {
		return fmt.Errorf("no host %s (nimble host list)", host)
	}
	t, err := config.TemplateFor(name, h)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, x := range t.Tools {
		keep[x] = true
	}
	var dropped []string
	for _, x := range h.Tools {
		if !keep[x] {
			dropped = append(dropped, x)
		}
	}
	shell := t.Shell
	if shell == "" {
		shell = "the system's"
	}
	nimble.Say("copy template " + name + " into hosts/" + host + ".yaml: tools [" + strings.Join(t.Tools, ", ") + "], shell " + shell)
	if len(dropped) == 0 {
		return nil
	}
	nimble.Warn("no longer in " + host + "'s tools: " + strings.Join(dropped, ", ") + " (they stay installed; nimble tool select uninstalls)")
	if !nimble.Interactive() {
		return nil
	}
	yes, err := nimble.ConfirmOrCancel("Replace "+host+"'s tools with template "+name+"'s?", false)
	if err != nil {
		return err
	}
	if !yes {
		return nimble.ErrCancelled
	}
	return nil
}

func newEdit() *cobra.Command {
	return &cobra.Command{
		Use:               "edit <name>",
		Short:             "Open a host's YAML in your editor (VS Code on a desktop) and check it",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: hosts.CompleteHostNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "host", hostNames())
			if err != nil {
				return err
			}
			if err := nimble.EditableHost(name); err != nil {
				return err
			}
			if err := repo.Editor(config.HostFile(name)); err != nil {
				return err
			}
			if errs := config.Validate(setup.KnownTools()); len(errs) > 0 {
				for _, e := range errs {
					nimble.Warn(e.Error())
				}
				return errors.New("hosts/" + name + ".yaml has problems — fix them (nimble host edit " + name + ")")
			}
			regenerate()
			return nimble.Record("hosts: edit "+name, "hosts")
		},
	}
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the hosts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			hs, err := config.Hosts()
			if err != nil {
				return err
			}
			w := 4
			for _, h := range hs {
				w = max(w, len(h.Name))
			}
			fmt.Printf("  %-*s  %-11s %-44s %-13s %-7s %s\n", w, "name", "mode", "address", "domain", "shell", "tools")
			for _, h := range hs {
				mode := "managed"
				if !h.Managed() {
					mode = h.Mode
				}
				addr := h.Address
				if addr != "" {
					addr = h.AdminUser() + "@" + addr
				}
				if h.Jump != "" {
					addr += " via " + h.Jump
				}
				shell, tools := h.Shell, strings.Join(h.Tools, ",")
				switch {
				case !h.Managed() && !h.Workstation():
					shell, tools = "-", "-" // nimble installs nothing there
				case shell == "":
					shell = "system"
				}
				dom := h.Domain
				if dom == "" && h.Managed() {
					d, _ := c.Domain("")
					dom = d.Name
				}
				fmt.Printf("  %-*s  %-11s %-44s %-13s %-7s %s\n", w, h.Name, mode, addr, dom, shell, tools)
			}
			return nil
		},
	}
}

func newShow() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print a host's configuration with the defaults it leaves out",
		Long: heredoc.Doc(`
			Print what applies to a host: its own settings (tools and shell included — a
			template was copied in) and the defaults it leaves out. A value without a comment
			comes from the host file; the others say where they come from. The host file's own
			comments at the top are kept. nimble host edit opens the file itself.
		`),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: hosts.CompleteHostNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "host", hostNames())
			if err != nil {
				return err
			}
			b, err := os.ReadFile(config.HostFile(name))
			if err != nil {
				return fmt.Errorf("no host %s", name)
			}
			h, ok := config.FindHost(name)
			if !ok {
				return fmt.Errorf("no host %s", name)
			}
			c, err := config.Load()
			if err != nil {
				return err
			}
			out, err := merged(c, h, headComments(string(b)))
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

// headComments is the comment block a host file starts with.
func headComments(file string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(file, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			break
		}
		b.WriteString(l)
	}
	return b.String()
}

// merged is a host's configuration as nimble applies it: the host file's settings and the
// defaults it leaves out. A value from the host file has no comment; every other value
// says where it comes from.
func merged(c *config.Config, h config.Host, head string) (string, error) {
	var b strings.Builder
	b.WriteString(head)
	line := func(kv, from string) {
		if from == "" {
			fmt.Fprintln(&b, kv)
		} else {
			fmt.Fprintf(&b, "%-30s # %s\n", kv, from)
		}
	}
	or := func(set bool, from string) string { // "" when the host file sets it
		if set {
			return ""
		}
		return from
	}

	if h.Address != "" {
		line("address: "+h.Address, "")
	}
	// the admin user and the domain matter for machines nimble reaches over ssh, not for a
	// workstation: there they show only when the file sets them
	if h.User != "" || !h.Workstation() {
		line("user: "+h.AdminUser(), or(h.User != "", "default"))
	}
	if h.Domain != "" || !h.Workstation() {
		d, err := c.Domain(h.Domain)
		if err != nil {
			return "", err
		}
		line("domain: "+d.Name, or(h.Domain != "", "default: the first in nimble.yaml"))
	}
	mode := h.Mode
	if mode == "" {
		mode = "managed"
	}
	line("mode: "+mode, or(h.Mode != "", "default"))
	if h.Jump != "" {
		line("jump: "+h.Jump, "")
	}
	if !h.Managed() && !h.Workstation() {
		b.WriteString("# reached over ssh only: nimble installs nothing here, no tools or hardening\n")
		return b.String(), nil
	}

	if len(h.Tools) == 0 {
		line("tools: []", "none")
	} else {
		b.WriteString("tools:\n")
		for _, t := range h.Tools {
			line("  - "+t, "")
		}
	}
	if h.Shell != "" {
		line("shell: "+h.Shell, "")
	} else {
		b.WriteString("# shell: the system's default login shell (the host file sets none)\n")
	}

	if h.Managed() {
		line("harden: "+h.HardenLevel(), or(h.Harden != "", "default"))
		if len(h.SudoAllowed) > 0 {
			line("sudoAllowed: ["+strings.Join(h.SudoAllowed, ", ")+"]", "")
		}
		if len(h.Services) > 0 {
			b.WriteString("services:\n")
			for _, s := range h.Services {
				fmt.Fprintf(&b, "  - {service: %s, repo: %s, state: %s}\n", s.Service, s.Repo, s.State)
			}
		}
		if len(h.Reach) > 0 {
			// what its ssh config holds: "all" expanded, and the hosts on the way
			hs, _ := config.Hosts()
			var got []string
			for _, l := range strings.Split(hosts.HostSSHConfig(h, hs), "\n") {
				if n, ok := strings.CutPrefix(l, "Host "); ok {
					got = append(got, n)
				}
			}
			line("reach: ["+strings.Join(h.Reach, ", ")+"]", "ssh entries here: "+strings.Join(got, ", "))
		}
	}
	return b.String(), nil
}

func newRemove() *cobra.Command {
	return &cobra.Command{
		Use:               "remove <name>",
		Short:             "Remove a host from the inventory (the host itself is not changed)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: hosts.CompleteHostNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "host", hostNames())
			if err != nil {
				return err
			}
			before, _ := os.ReadFile(config.HostFile(name))
			if err := config.RemoveHost(name); err != nil {
				return err
			}
			// another host reached through it, or reaching it by name: those go first
			for _, e := range config.Validate(setup.KnownTools()) {
				if where, _, ok := strings.Cut(e.Error(), ": no host "+name+" in hosts/"); ok {
					_ = os.WriteFile(config.HostFile(name), before, 0o644)
					file, key, _ := strings.Cut(where, ": ") // hosts/grizzly.yaml: jump
					other := strings.TrimSuffix(strings.TrimPrefix(file, "hosts/"), ".yaml")
					return fmt.Errorf("%s is the %s of %s — change that first: nimble host set %s %s=…", name, key, other, other, key)
				}
			}
			nimble.Say("removed hosts/" + name + ".yaml")
			regenerate()
			return nimble.Record("hosts: remove "+name, "hosts")
		},
	}
}

func hostNames() []string {
	hs, _ := config.Hosts()
	var names []string
	for _, h := range hs {
		names = append(names, h.Name)
	}
	return names
}
