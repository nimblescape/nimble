package host

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/initialize"
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
			user, domain, mode (managed, ssh, workstation), template, extra tools, login shell
			and services. The workstation's ssh config is generated from it.
		`),
	}
	cmd.AddCommand(newCreate(), newSet(), newEdit(), newList(), newShow(), newRemove())
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
	var noInit bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Add a host (guided), then offer to set it up",
		Long: heredoc.Doc(`
			Write hosts/<name>.yaml — asking for whatever the flags don't give — regenerate
			the ssh config, commit, then offer nimble init <name>.
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
			if h.Address == "" && h.Mode != "workstation" {
				if h.Address = nimble.Ask("Address (hostname or IP)?", ""); h.Address == "" {
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
			if h.Template == "" && h.Mode != "ssh" {
				ts, _ := config.Templates()
				var names []string
				for _, t := range ts {
					names = append(names, t.Name)
				}
				if t := nimble.Ask("Template ("+strings.Join(names, ", ")+"; empty = the default)?", ""); t != "" {
					h.Template = t
				}
			}
			if h.Template != "" {
				if _, err := config.FindTemplate(h.Template); err != nil {
					return err
				}
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
	cmd.Flags().StringVar(&h.Template, "template", "", "template (default: the default template)")
	cmd.Flags().StringVar(&h.Mode, "mode", "", "managed, ssh or workstation")
	cmd.Flags().BoolVar(&noInit, "no-init", false, "don't offer to set the host up")
	return cmd
}

func newSet() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> key=value…",
		Short: "Change a host's settings (address, user, domain, mode, template, tools, shell)",
		Example: heredoc.Doc(`
			nimble host set devbox template=dev
			nimble host set devbox tools=docker,gh
			nimble host set devbox shell=bash
		`),
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: hosts.CompleteHostNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "host", hostNames())
			if err != nil {
				return err
			}
			if err := nimble.EditableHost(name); err != nil {
				return err
			}
			settings := args[min(1, len(args)):]
			if len(settings) == 0 {
				s, err := nimble.Arg(nil, 0, "change (key=value; keys: address, user, domain, mode, template, tools, shell)", nil)
				if err != nil {
					return err
				}
				settings = []string{s}
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
			if self, ok := hosts.Self(""); ok && self.Name == name {
				nimble.Step("apply it: nimble update")
			} else {
				nimble.Step("apply it: nimble update " + name)
			}
			return nil
		},
	}
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
			fmt.Printf("  %-*s  %-11s %-44s %-13s %s\n", w, "name", "mode", "address", "domain", "template")
			for _, h := range hs {
				mode := "managed"
				if !h.Managed() {
					mode = h.Mode
				}
				addr := h.Address
				if addr != "" {
					addr = h.AdminUser() + "@" + addr
				}
				tmpl := h.Template
				if tmpl == "" && (h.Managed() || h.Workstation()) {
					tmpl = c.DefaultTemplate + " (default)"
				}
				dom := h.Domain
				if dom == "" && h.Managed() {
					d, _ := c.Domain("")
					dom = d.Name
				}
				fmt.Printf("  %-*s  %-11s %-44s %-13s %s\n", w, h.Name, mode, addr, dom, tmpl)
			}
			return nil
		},
	}
}

func newShow() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print a host's configuration, merged with its template and the defaults",
		Long: heredoc.Doc(`
			Print what applies to a host: its own settings, the defaults it leaves out, and its
			template's tools and shell folded in. A value without a comment comes from the host
			file; the others say where they come from (default, template <name>). The host
			file's own comments at the top are kept. nimble host edit opens the file itself.
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

// merged is a host's configuration as nimble applies it: the host file's settings, the
// defaults it leaves out, and its template's tools and shell. A value from the host file
// has no comment; every other value says where it comes from.
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
	if !h.Managed() && !h.Workstation() {
		b.WriteString("# reached over ssh only: nimble installs nothing here, no template or hardening\n")
		return b.String(), nil
	}

	r, err := config.Merge(c, &h)
	if err != nil {
		return "", err
	}
	if r.Template == "" {
		b.WriteString("# no template (nimble.yaml has no defaultTemplate): only the host's own tools\n")
	} else {
		line("template: "+r.Template, or(h.Template != "", "default: defaultTemplate in nimble.yaml"))
	}
	fromTemplate := map[string]bool{}
	for _, t := range r.TemplateTools {
		fromTemplate[t] = true
	}
	if len(r.Tools) == 0 {
		line("tools: []", or(r.Template == "", "template "+r.Template+" has none"))
	} else {
		b.WriteString("tools:\n")
		for _, t := range r.Tools {
			line("  - "+t, or(!fromTemplate[t], "template "+r.Template))
		}
	}
	switch {
	case h.Shell != "":
		line("shell: "+h.Shell, "")
	case r.Shell != "":
		line("shell: "+r.Shell, "template "+r.Template)
	default:
		b.WriteString("# shell: the system's default login shell (neither the template nor the host sets one)\n")
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
			if err := config.RemoveHost(name); err != nil {
				return err
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
