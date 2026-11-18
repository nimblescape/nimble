// Package hosts runs nimble on the managed hosts over ssh.
package hosts

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

// List expands "all" to every managed host in hosts/.
func List(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		if a != "all" {
			out = append(out, a)
			continue
		}
		hs, err := config.Hosts()
		if err != nil {
			return nil, err
		}
		n := len(out)
		for _, h := range hs {
			if h.Managed() {
				out = append(out, h.Name)
			}
		}
		if len(out) == n {
			return nil, errors.New("no managed hosts in " + nimble.Tilde(nimble.RepoFile("hosts")))
		}
	}
	return out, nil
}

// Opts tune an ssh call.
type Opts struct {
	Agent bool      // forward the SSH agent (sudo via Touch ID)
	Fresh bool      // no connection sharing: really authenticate
	Stdin io.Reader // instead of the terminal
}

// Run runs a command on a host; a terminal is attached when nimble has one.
func Run(host string, o Opts, remote string) error {
	var args []string
	if o.Fresh {
		args = append(args, "-o", "ControlPath=none")
	}
	if o.Stdin == nil && nimble.IsTerminal(os.Stdin) {
		args = append(args, "-t")
	}
	if o.Agent {
		args = append(args, "-A")
	}
	cmd := SSH(append(args, host, remote)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, nimble.Stdout(), nimble.Stderr() // under a Wait: stop its spinner first
	if o.Stdin != nil {
		cmd.Stdin = o.Stdin
	}
	return nimble.Exec(cmd)
}

// Output runs a command on a host and returns its trimmed stdout ("" if it fails).
func Output(host, remote string) string {
	cmd := SSH(host, remote)
	cmd.Stderr = nimble.Stderr() // a YubiKey's PIN prompt
	out, err := nimble.ExecOutput(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// User is the login user for a host, as ssh resolves it.
func User(host string) string {
	for _, line := range strings.Split(nimble.Output("ssh", "-G", host), "\n") {
		if strings.HasPrefix(line, "user ") {
			return strings.TrimPrefix(line, "user ")
		}
	}
	return ""
}

// Nimble runs a nimble command on a host (nimble sets up its own environment there).
func Nimble(host string, o Opts, args string) error {
	return Run(host, o, `"$HOME/.local/bin/nimble" `+args)
}

// Each runs fn for every host under a banner and reports how many failed.
func Each(hosts []string, fn func(host string) error) error {
	failed := 0
	for _, h := range hosts {
		nimble.Banner(h)
		if err := fn(h); err != nil {
			if !errors.Is(err, nimble.ErrProblems) {
				nimble.Step(nimble.Red("failed: " + err.Error()))
			}
			failed++
		}
	}
	if failed > 0 {
		return nimble.ErrProblems
	}
	return nil
}

// LocalOrRemote runs a check here (no hosts given) or as `nimble <sub>` on each host.
func LocalOrRemote(args []string, sub string, local func() error) error {
	if len(args) == 0 {
		return local()
	}
	hs, err := List(args)
	if err != nil {
		return err
	}
	return Each(hs, func(h string) error { return Nimble(h, Opts{}, sub) })
}

// Complete offers "all", the hosts in hosts/ and the Host aliases from ~/.ssh/config.
func Complete(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	seen := map[string]bool{}
	for _, a := range args {
		seen[a] = true
	}
	var out []string
	add := func(name, desc string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name+"\t"+desc)
		}
	}
	add("all", "every managed host")
	if hs, err := config.Hosts(); err == nil {
		for _, h := range hs {
			switch {
			case h.Workstation():
			case h.Managed():
				add(h.Name, "managed host")
			default:
				add(h.Name, "ssh entry")
			}
		}
	}
	if f, err := os.Open(filepath.Join(nimble.Home(), ".ssh", "config")); err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			fields := strings.Fields(s.Text())
			if len(fields) >= 2 && strings.EqualFold(fields[0], "Host") {
				for _, h := range fields[1:] {
					if !strings.ContainsAny(h, "*?!") {
						add(h, "ssh host")
					}
				}
			}
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// CompleteHostNames offers the names in hosts/ (for host subcommands).
func CompleteHostNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	hs, _ := config.Hosts()
	var out []string
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// Self is this machine's host file: the workstation's (named after the machine) or, on a
// Debian host, the name the workstation passed (--as), remembered in ~/.nimble/cache/host.
func Self(as string) (config.Host, bool) {
	file := nimble.CacheDir("host")
	if as != "" && nimble.ReadTrim(file) != as && os.Geteuid() != 0 { // root (sudo) never writes into the user's files
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		_ = os.WriteFile(file, []byte(as+"\n"), 0o644)
	}
	if as == "" {
		as = nimble.ReadTrim(file)
	}
	if as == "" {
		as = MachineName()
	}
	return config.FindHost(as)
}

// MachineName is a short, file-name friendly name for this machine.
func MachineName() string {
	n := nimble.Output("scutil", "--get", "LocalHostName")
	if n == "" {
		n, _ = os.Hostname()
	}
	return strings.ToLower(strings.Split(n, ".")[0])
}

// SSHConfig renders the ssh client entries for all hosts: each uses the daily key of
// its domain; managed hosts forward the agent (sudo there asks for Touch ID).
func SSHConfig(c *config.Config, handle func(label string) string) string {
	hs, _ := config.Hosts()
	var b strings.Builder
	for _, h := range hs {
		if h.Workstation() || h.Address == "" {
			continue
		}
		b.WriteString("Host " + h.Name + "\n  HostName " + h.Address + "\n  User " + h.AdminUser() + "\n")
		if d, err := c.Domain(h.Domain); err == nil {
			b.WriteString("  IdentityFile " + nimble.Tilde(handle(d.Label)) + "\n")
			if old := handle(d.Label) + ".old"; nimble.Exists(old) { // a renewed daily key: until this host has the new one
				b.WriteString("  IdentityFile " + nimble.Tilde(old) + "\n")
			}
		}
		if h.Managed() {
			b.WriteString("  ForwardAgent yes\n  AddKeysToAgent yes\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
