// Package config reads and writes the YAML inventories of the nimble config repo:
//
//	nimble.yaml              default template, domains, linked config files
//	hosts/<name>.yaml        one file per machine (workstations included): its tools, shell, services
//	templates/<name>.yaml    starting points: tools and login shell, copied into a host file
//	toolchain/pins.yaml      pinned versions (see pins.go)
//
// Writes go through the parsed YAML tree, so comments in the files survive.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nimblescape/nimble/pkg/nimble"
	"gopkg.in/yaml.v3"
)

// Config is nimble.yaml.
type Config struct {
	Format          int     `yaml:"format,omitempty"` // 2: host files hold their tools and shell (see MigrateHosts)
	DefaultTemplate string  `yaml:"defaultTemplate"`
	Domains         Domains `yaml:"domains"`
	Links           []Link  `yaml:"links"`
}

// Domain is an administration domain: who runs the machines, and the label of its
// daily key (a Secure Enclave key on every workstation).
type Domain struct {
	Name  string
	Label string `yaml:"label"`
}

// Domains keeps the order of nimble.yaml; the first one is the default.
type Domains []Domain

// UnmarshalYAML reads the `domains:` mapping in file order.
func (d *Domains) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return errors.New("domains: expected a mapping of name → {label: …}")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		var dom Domain
		if err := n.Content[i+1].Decode(&dom); err != nil {
			return fmt.Errorf("domain %s: %w", n.Content[i].Value, err)
		}
		dom.Name = n.Content[i].Value
		*d = append(*d, dom)
	}
	return nil
}

// Link is a config file linked from the repo into $HOME.
type Link struct {
	From string `yaml:"from"` // path in the repo (or in the personal layer)
	To   string `yaml:"to"`   // path under ~
	OS   string `yaml:"os"`   // "", "macos" or "debian"
	Base string `yaml:"-"`    // the layer the link comes from
}

// Source is the file the link points to.
func (l Link) Source() string { return filepath.Join(l.Base, l.From) }

// PersonalDir is the personal layer, ~/.nimble/personal: your own settings (git identity,
// aliases, app settings) on top of the organization's shared config. Optional.
func PersonalDir() string { return nimble.RepoFile("personal") }

// Load reads nimble.yaml, plus the links of the personal layer's nimble.yaml.
func Load() (*Config, error) {
	var c Config
	if err := readYAML(nimble.RepoFile("nimble.yaml"), &c); err != nil {
		return nil, fmt.Errorf("%w (is %s a nimble config repo?)", err, nimble.Tilde(nimble.Repo()))
	}
	for i := range c.Links {
		c.Links[i].Base = nimble.Repo()
	}
	var personal struct {
		Links []Link `yaml:"links"`
	}
	if p := filepath.Join(PersonalDir(), "nimble.yaml"); nimble.Exists(p) {
		if err := readYAML(p, &personal); err != nil {
			return nil, err
		}
		for _, l := range personal.Links {
			l.Base = PersonalDir()
			c.Links = append(c.Links, l)
		}
	}
	return &c, nil
}

// Domain looks a domain up by name ("" = the default, the first one).
func (c *Config) Domain(name string) (Domain, error) {
	if len(c.Domains) == 0 {
		return Domain{}, errors.New("no domains in nimble.yaml")
	}
	if name == "" {
		return c.Domains[0], nil
	}
	for _, d := range c.Domains {
		if d.Name == name {
			return d, nil
		}
	}
	return Domain{}, fmt.Errorf("unknown domain %q (nimble.yaml lists: %s)", name, strings.Join(c.DomainNames(), ", "))
}

// DomainNames lists the domains in order.
func (c *Config) DomainNames() []string {
	var out []string
	for _, d := range c.Domains {
		out = append(out, d.Name)
	}
	return out
}

// OS is this system as nimble names it: "macos" or "debian" ("" elsewhere).
func OS() string {
	switch {
	case nimble.OSName() == "Darwin":
		return "macos"
	case nimble.IsDebian():
		return "debian"
	}
	return ""
}

// --- hosts ----------------------------------------------------------------------------

// Host is hosts/<name>.yaml.
type Host struct {
	Name     string    `yaml:"-"`
	Address  string    `yaml:"address,omitempty"`
	User     string    `yaml:"user,omitempty"`
	Domain   string    `yaml:"domain,omitempty"`
	Mode     string    `yaml:"mode,omitempty"`     // managed (default) | ssh | workstation
	Template string    `yaml:"template,omitempty"` // only before format 2: MigrateHosts copies it in
	Tools    []string  `yaml:"tools,omitempty"`
	Shell    string    `yaml:"shell,omitempty"`
	Services []Service `yaml:"services,omitempty"`
	// SudoAllowed: accounts besides the admin user that may sudo on purpose (e.g. a service
	// account with passwordless sudo); harden leaves them and their sudo rules alone.
	SudoAllowed []string `yaml:"sudoAllowed,omitempty"`
	// Harden: full (default: root locked, keys only, only the admin may sudo, with a hardware
	// key) or sudo (only sudo confirmed with a hardware key — for hosts others administer).
	Harden string `yaml:"harden,omitempty"`
	// Jump: the host (in hosts/) this one is reached through (ssh's ProxyJump), from the
	// workstation and from other hosts; its address is resolved there.
	Jump string `yaml:"jump,omitempty"`
	// Reach: the hosts (in hosts/, or "all") this host gets ssh entries for, in its own
	// ssh config. They log in with the workstation's key through the forwarded agent.
	Reach []string `yaml:"reach,omitempty"`
}

// HardenLevel is the host's hardening: "full" or "sudo".
func (h Host) HardenLevel() string {
	if h.Harden == "" {
		return "full"
	}
	return h.Harden
}

// Service is a long-running service for a repo on a host (e.g. a Claude Code server).
type Service struct {
	Service string `yaml:"service"`
	Repo    string `yaml:"repo"`
	State   string `yaml:"state"` // enabled | disabled
}

// Managed hosts are set up and kept up to date by nimble ("all").
func (h Host) Managed() bool { return h.Mode == "" || h.Mode == "managed" }

// Workstation reports whether the file describes a workstation.
func (h Host) Workstation() bool { return h.Mode == "workstation" }

// AdminUser is the user nimble sets up on the host (default debian).
func (h Host) AdminUser() string {
	if h.User == "" {
		return "debian"
	}
	return h.User
}

// Dest is user@address for ssh.
func (h Host) Dest() string { return h.AdminUser() + "@" + h.Address }

func hostPath(name string) string { return nimble.RepoFile("hosts", name+".yaml") }

// Hosts lists hosts/*.yaml, sorted by name.
func Hosts() ([]Host, error) {
	files, _ := filepath.Glob(nimble.RepoFile("hosts", "*.yaml"))
	sort.Strings(files)
	var out []Host
	for _, f := range files {
		var h Host
		if err := readYAML(f, &h); err != nil {
			return nil, err
		}
		h.Name = strings.TrimSuffix(filepath.Base(f), ".yaml")
		out = append(out, h)
	}
	return out, nil
}

// FindHost reads hosts/<name>.yaml.
func FindHost(name string) (Host, bool) {
	var h Host
	if name == "" || readYAML(hostPath(name), &h) != nil {
		return Host{}, false
	}
	h.Name = name
	return h, true
}

// HostFile is the path of a host's YAML.
func HostFile(name string) string { return hostPath(name) }

// CreateHost writes a new hosts/<name>.yaml.
func CreateHost(h Host) error {
	if nimble.Exists(hostPath(h.Name)) {
		return fmt.Errorf("hosts/%s.yaml already exists", h.Name)
	}
	return writeYAML(hostPath(h.Name), h, "")
}

// RemoveHost deletes hosts/<name>.yaml.
func RemoveHost(name string) error {
	if !nimble.Exists(hostPath(name)) {
		return fmt.Errorf("no host %s (hosts/%s.yaml)", name, name)
	}
	return os.Remove(hostPath(name))
}

// SetHost changes one key of a host's YAML, keeping the rest of the file (and comments).
func SetHost(name, key, value string) error {
	if !nimble.Exists(hostPath(name)) {
		return fmt.Errorf("no host %s", name)
	}
	switch key {
	case "address", "user", "domain", "mode", "shell", "jump":
	case "reach":
		return EditYAML(hostPath(name), func(doc *yaml.Node) error { return setList(doc, "reach", splitList(value)) })
	case "template": // a copy: the template's tools and shell replace the host's
		h, _ := FindHost(name)
		t, err := TemplateFor(value, h)
		if err != nil {
			return err
		}
		return EditYAML(hostPath(name), func(doc *yaml.Node) error { return copyTemplate(doc, t.Tools, t.Shell) })
	case "tools":
		return EditYAML(hostPath(name), func(doc *yaml.Node) error { return setList(doc, "tools", splitList(value)) })
	default:
		return fmt.Errorf("unknown host setting %q (address, user, domain, mode, template, tools, shell, jump, reach)", key)
	}
	return EditYAML(hostPath(name), func(doc *yaml.Node) error { return setScalar(doc, key, value) })
}

// SetHostServices replaces a host's services list.
func SetHostServices(name string, services []Service) error {
	return EditYAML(hostPath(name), func(doc *yaml.Node) error {
		var n yaml.Node
		if err := n.Encode(services); err != nil {
			return err
		}
		return setNode(doc, "services", &n, len(services) == 0)
	})
}

// --- templates ------------------------------------------------------------------------

// Template is templates/<name>.yaml.
type Template struct {
	Name        string   `yaml:"-"`
	Description string   `yaml:"description,omitempty"`
	OS          string   `yaml:"os,omitempty"`    // "" (both), macos, debian
	Shell       string   `yaml:"shell,omitempty"` // "" (system default), zsh, bash
	Tools       []string `yaml:"tools,omitempty"`
}

func templatePath(name string) string { return nimble.RepoFile("templates", name+".yaml") }

// TemplateFile is the path of a template's YAML.
func TemplateFile(name string) string { return templatePath(name) }

// Templates lists templates/*.yaml, sorted by name.
func Templates() ([]Template, error) {
	files, _ := filepath.Glob(nimble.RepoFile("templates", "*.yaml"))
	sort.Strings(files)
	var out []Template
	for _, f := range files {
		var t Template
		if err := readYAML(f, &t); err != nil {
			return nil, err
		}
		t.Name = strings.TrimSuffix(filepath.Base(f), ".yaml")
		out = append(out, t)
	}
	return out, nil
}

// FindTemplate reads templates/<name>.yaml.
func FindTemplate(name string) (Template, error) {
	var t Template
	if err := readYAML(templatePath(name), &t); err != nil {
		return t, fmt.Errorf("template %s: %w", name, err)
	}
	t.Name = name
	return t, nil
}

// SetDefaultTemplate writes defaultTemplate into nimble.yaml.
func SetDefaultTemplate(name string) error {
	if _, err := FindTemplate(name); err != nil {
		return err
	}
	return EditYAML(nimble.RepoFile("nimble.yaml"), func(doc *yaml.Node) error { return setScalar(doc, "defaultTemplate", name) })
}

// AddDomain appends a domain to nimble.yaml.
func AddDomain(name, label string) error {
	return EditYAML(nimble.RepoFile("nimble.yaml"), func(doc *yaml.Node) error {
		root := doc.Content[0]
		doms := mapValue(root, "domains")
		if doms == nil {
			doms = &yaml.Node{Kind: yaml.MappingNode}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "domains"}, doms)
		}
		if mapValue(doms, name) != nil {
			return fmt.Errorf("domain %s exists", name)
		}
		val := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "label"}, {Kind: yaml.ScalarNode, Value: label}}}
		doms.Content = append(doms.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, val)
		return nil
	})
}

// SetTemplate changes one key of a template's YAML (description, os, shell, tools).
func SetTemplate(name, key, value string) error {
	path := templatePath(name)
	if !nimble.Exists(path) {
		return fmt.Errorf("no template %s", name)
	}
	switch key {
	case "description", "os", "shell":
		return EditYAML(path, func(doc *yaml.Node) error { return setScalar(doc, key, value) })
	case "tools":
		return EditYAML(path, func(doc *yaml.Node) error { return setList(doc, "tools", splitList(value)) })
	}
	return fmt.Errorf("unknown template setting %q (description, os, shell, tools)", key)
}

// CreateTemplate writes a new templates/<name>.yaml.
func CreateTemplate(t Template) error {
	if nimble.Exists(templatePath(t.Name)) {
		return fmt.Errorf("templates/%s.yaml exists", t.Name)
	}
	return writeYAML(templatePath(t.Name), t, "")
}

// Validate checks the whole config repo: nimble.yaml, every template and every host.
// tools are the names in the tool catalog.
func Validate(tools []string) []error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	known := map[string]bool{}
	for _, t := range tools {
		known[t] = true
	}
	c, err := Load()
	if err != nil {
		return []error{err}
	}
	if len(c.Domains) == 0 {
		bad("nimble.yaml: no domains")
	}
	for _, d := range c.Domains {
		if err := ValidLabel(d.Label); err != nil {
			bad("nimble.yaml: domain %s: %v", d.Name, err)
		}
	}
	templates := map[string]bool{}
	ts, err := Templates()
	if err != nil {
		errs = append(errs, err)
	}
	for _, t := range ts {
		templates[t.Name] = true
		if t.OS != "" && t.OS != "macos" && t.OS != "debian" {
			bad("templates/%s.yaml: os %q (macos or debian)", t.Name, t.OS)
		}
		if t.Shell != "" && t.Shell != "zsh" && t.Shell != "bash" {
			bad("templates/%s.yaml: shell %q (zsh or bash)", t.Name, t.Shell)
		}
		for _, tool := range t.Tools {
			if !known[tool] {
				bad("templates/%s.yaml: unknown tool %q", t.Name, tool)
			}
		}
	}
	if c.DefaultTemplate != "" && !templates[c.DefaultTemplate] {
		bad("nimble.yaml: defaultTemplate %q doesn't exist", c.DefaultTemplate)
	}
	for _, l := range c.Links {
		if l.OS != "" && l.OS != "macos" && l.OS != "debian" {
			bad("links: %s: os %q (macos or debian)", l.From, l.OS)
		}
		if !nimble.Exists(l.Source()) {
			bad("links: %s doesn't exist", nimble.Tilde(l.Source()))
		}
	}
	errs = append(errs, validatePins()...)
	hs, err := Hosts()
	if err != nil {
		errs = append(errs, err)
	}
	for _, h := range hs {
		f := "hosts/" + h.Name + ".yaml"
		switch h.Mode {
		case "", "managed", "ssh", "workstation":
		default:
			bad("%s: mode %q (managed, ssh or workstation)", f, h.Mode)
		}
		if h.Harden != "" && h.Harden != "full" && h.Harden != "sudo" {
			bad("%s: harden %q (full or sudo)", f, h.Harden)
		}
		if !h.Workstation() && h.Address == "" {
			bad("%s: no address", f)
		}
		// both go into the ssh config and into commands run on the host
		if !plain.MatchString(h.Address) && h.Address != "" {
			bad("%s: address %q (a hostname or IP address)", f, h.Address)
		}
		if !plain.MatchString(h.User) && h.User != "" {
			bad("%s: user %q (a login name)", f, h.User)
		}
		if _, err := c.Domain(h.Domain); err != nil {
			bad("%s: %v", f, err)
		}
		if h.Template != "" { // templates are copied into the host file, not named
			bad("%s: template: %s — a host file holds its tools and shell itself: nimble host set %s template=%s copies them in", f, h.Template, h.Name, h.Template)
		}
		if h.Shell != "" && h.Shell != "zsh" && h.Shell != "bash" {
			bad("%s: shell %q (zsh or bash)", f, h.Shell)
		}
		for _, tool := range h.Tools {
			if !known[tool] {
				bad("%s: unknown tool %q", f, tool)
			}
		}
		for _, s := range h.Services {
			if s.Service != "claude" || s.Repo == "" || (s.State != "enabled" && s.State != "disabled") {
				bad("%s: service %+v (service claude, a repo, state enabled or disabled)", f, s)
			}
		}
	}
	errs = append(errs, validateSSH(hs)...)
	errs = append(errs, validateAliases()...)
	return errs
}

// validateSSH checks jump and reach. A snapshot on a host holds only its own host file: the
// hosts named there are checked on the workstation.
func validateSSH(hs []Host) []error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	byName := map[string]Host{}
	for _, h := range hs {
		byName[h.Name] = h
	}
	snapshot := nimble.IsSnapshot()
	// target is a host that can be connected to: in hosts/, not a workstation, with an address
	target := func(f, key, name string) {
		t, ok := byName[name]
		switch {
		case !ok && snapshot:
		case !ok:
			bad("%s: %s: no host %s in hosts/", f, key, name)
		case t.Workstation():
			bad("%s: %s: %s is a workstation", f, key, name)
		case t.Address == "":
			bad("%s: %s: %s has no address", f, key, name)
		}
	}
	for _, h := range hs {
		f := "hosts/" + h.Name + ".yaml"
		if h.Jump != "" {
			switch {
			case !plain.MatchString(h.Jump):
				bad("%s: jump %q (a host name)", f, h.Jump)
			case h.Jump == h.Name:
				bad("%s: jump: a host can't be reached through itself", f)
			case h.Workstation():
				bad("%s: jump: a workstation isn't reached over ssh", f)
			default:
				target(f, "jump", h.Jump)
			}
			// a loop (a → b → a) would never connect
			seen := map[string]bool{h.Name: true}
			for j := h.Jump; j != ""; j = byName[j].Jump {
				if seen[j] {
					bad("%s: jump: %s leads back in a loop", f, h.Jump)
					break
				}
				seen[j] = true
			}
		}
		if len(h.Reach) > 0 && !h.Managed() {
			bad("%s: reach: only a managed host gets an ssh config from nimble", f)
			continue
		}
		for _, r := range h.Reach {
			switch {
			case r == "all":
				if len(h.Reach) > 1 {
					bad("%s: reach: all stands alone", f)
				}
			case !plain.MatchString(r):
				bad("%s: reach %q (a host name or all)", f, r)
			case r == h.Name:
				bad("%s: reach: %s is the host itself", f, r)
			default:
				target(f, "reach", r)
			}
		}
	}
	return errs
}

var (
	plain   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	labelRe = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
	version = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// ValidLabel checks the label of a domain's daily key. It ends the key's file name
// (keys/enclave/<machine>-<label>.pub), so it has no "-" of its own.
func ValidLabel(label string) error {
	if !labelRe.MatchString(label) {
		return fmt.Errorf("label %q: letters, digits, '_' and '.' (no '-': it separates the machine from the label in keys/enclave/)", label)
	}
	return nil
}

// validatePins checks toolchain/pins.yaml: it parses, and the pinned languages are x.y.z.
func validatePins() []error {
	p, err := LoadPins()
	if err != nil {
		return []error{err}
	}
	var errs []error
	check := func(key, v string) {
		if v != "" && !version.MatchString(v) {
			errs = append(errs, fmt.Errorf("toolchain/pins.yaml: %s %q (a version x.y.z)", key, v))
		}
	}
	check("node", p.Node)
	check("go", p.Go)
	check("rust", p.Rust)
	check("python", p.Python)
	return errs
}

// Resolved is what applies to a machine: its host file's tools and shell — or, on a
// machine without a host file, the default template's.
type Resolved struct {
	Template string   // the default template a machine without a host file gets ("" otherwise)
	Tools    []string // nil = none
	Shell    string   // "" = the system default
}

// Resolve is what applies to a machine. A host file is complete in itself: templates were
// copied into it when it was made (host create, host set template=). A machine without a
// host file gets the default template, which must be for this machine's system.
func Resolve(c *Config, h *Host) (Resolved, error) {
	if h != nil {
		return Resolved{Tools: h.Tools, Shell: h.Shell}, nil
	}
	if c.DefaultTemplate == "" {
		return Resolved{}, nil
	}
	t, err := FindTemplate(c.DefaultTemplate)
	if err != nil {
		return Resolved{}, err
	}
	if t.OS != "" && OS() != "" && t.OS != OS() {
		return Resolved{}, fmt.Errorf("template %s is for %s, this is %s", t.Name, t.OS, OS())
	}
	return Resolved{Template: t.Name, Tools: t.Tools, Shell: t.Shell}, nil
}

// TemplateFor reads the template to copy into host h, checked against what h is: a
// managed host runs Debian, and nimble installs nothing on a host reached over ssh only.
func TemplateFor(name string, h Host) (Template, error) {
	if !h.Managed() && !h.Workstation() {
		return Template{}, fmt.Errorf("%s is reached over ssh only (mode %s): nimble installs nothing there, so it takes no template", h.Name, h.Mode)
	}
	t, err := FindTemplate(name)
	if err != nil {
		return t, err
	}
	if h.Managed() && t.OS == "macos" {
		return t, fmt.Errorf("template %s is for macos; %s is a managed Debian host", name, h.Name)
	}
	return t, nil
}

// copyTemplate writes tools and shell into a host file's tree: in place where the file has
// them, else where its template: line was (or at the end). An empty value removes the key;
// the template: line goes.
func copyTemplate(doc *yaml.Node, tools []string, shell string) error {
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("expected a mapping at the top of the file")
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, t := range tools {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: t})
	}
	vals := []struct {
		key   string
		node  *yaml.Node
		empty bool
	}{{"tools", seq, len(tools) == 0}, {"shell", &yaml.Node{Kind: yaml.ScalarNode, Value: shell}, shell == ""}}
	var missing []*yaml.Node
	for _, v := range vals {
		if mapValue(root, v.key) != nil || v.empty {
			if err := setNode(doc, v.key, v.node, v.empty); err != nil { // in place, or removed
				return err
			}
			continue
		}
		missing = append(missing, &yaml.Node{Kind: yaml.ScalarNode, Value: v.key}, v.node)
	}
	at := len(root.Content)
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "template" {
			at = i
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			break
		}
	}
	root.Content = append(root.Content[:at], append(missing, root.Content[at:]...)...)
	return nil
}

// Format 2: host files hold their tools and shell themselves (templates are copied in);
// before, a host file named its template — or, without one, got the default template.
const currentFormat = 2

// MigrateHosts copies the templates into the host files once, when nimble.yaml is older
// than format 2: a managed host's or workstation's tools become its template's (or the
// default template's) followed by its own, its shell its own else the template's; the
// template: line goes. Then nimble.yaml gets format: 2. It returns the hosts it changed,
// and whether it migrated at all.
func MigrateHosts() (changed []string, migrated bool, err error) {
	c, err := Load()
	if err != nil || c.Format >= currentFormat {
		return nil, false, err
	}
	hs, err := Hosts()
	if err != nil {
		return nil, false, err
	}
	for _, h := range hs {
		name := h.Template
		if name == "" && (h.Managed() || h.Workstation()) {
			name = c.DefaultTemplate
		}
		if name == "" {
			continue
		}
		tools, shell := h.Tools, h.Shell
		if h.Managed() || h.Workstation() {
			t, err := FindTemplate(name)
			if err != nil {
				return nil, false, fmt.Errorf("hosts/%s.yaml: %w", h.Name, err)
			}
			tools = appendUnique(append([]string{}, t.Tools...), h.Tools...)
			if shell == "" {
				shell = t.Shell
			}
		}
		if err := EditYAML(hostPath(h.Name), func(doc *yaml.Node) error { return copyTemplate(doc, tools, shell) }); err != nil {
			return nil, false, err
		}
		changed = append(changed, h.Name)
	}
	err = EditYAML(nimble.RepoFile("nimble.yaml"), func(doc *yaml.Node) error {
		return setScalar(doc, "format", fmt.Sprint(currentFormat))
	})
	return changed, true, err
}

func appendUnique(list []string, more ...string) []string {
	for _, m := range more {
		found := false
		for _, l := range list {
			if l == m {
				found = true
			}
		}
		if !found {
			list = append(list, m)
		}
	}
	return list
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" && s != "none" {
			out = append(out, s)
		}
	}
	return out
}

// --- YAML helpers -----------------------------------------------------------------------

// readYAML decodes strictly: an unknown key (a typo such as "tool:") is an error.
func readYAML(path string, into any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil && err != io.EOF {
		return fmt.Errorf("%s: %w", nimble.Tilde(path), err)
	}
	return nil
}

func writeYAML(path string, v any, header string) error {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return err
	}
	return writeNode(path, &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&n}, HeadComment: header})
}

func writeNode(path string, doc *yaml.Node) error {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// EditYAML changes a YAML file through its node tree, so comments and order survive.
func EditYAML(path string, edit func(doc *yaml.Node) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("%s: %w", nimble.Tilde(path), err)
	}
	if doc.Kind == 0 { // empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if err := edit(&doc); err != nil {
		return err
	}
	return writeNode(path, &doc)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setNode(doc *yaml.Node, key string, val *yaml.Node, remove bool) error {
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("expected a mapping at the top of the file")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			if remove {
				root.Content = append(root.Content[:i], root.Content[i+2:]...)
				return nil
			}
			val.LineComment = root.Content[i+1].LineComment
			root.Content[i+1] = val
			return nil
		}
	}
	if !remove {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, val)
	}
	return nil
}

func setScalar(doc *yaml.Node, key, value string) error {
	return setNode(doc, key, &yaml.Node{Kind: yaml.ScalarNode, Value: value}, value == "")
}

func setList(doc *yaml.Node, key string, items []string) error {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, it := range items {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: it})
	}
	return setNode(doc, key, seq, len(items) == 0)
}
