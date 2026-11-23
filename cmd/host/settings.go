package host

import (
	"errors"
	"os"
	"strings"

	"github.com/nimblescape/nimble/cmd/keyval"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/setup"
)

// settings are host set's keys for host h, with h's values: what tab completion offers and
// the guided menu shows.
func settings(h config.Host) []keyval.Setting {
	hs, _ := config.Hosts()
	others := []keyval.Choice{}
	for _, o := range hs {
		if o.Name != h.Name && !o.Workstation() && o.Address != "" {
			desc := o.AdminUser() + "@" + o.Address
			if o.Jump != "" {
				desc += " via " + o.Jump
			}
			others = append(others, keyval.Choice{Value: o.Name, Desc: desc})
		}
	}
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}

	addrHelp := "hostname or IP address"
	if h.Jump != "" {
		addrHelp += ", as seen from " + h.Jump
	}
	out := []keyval.Setting{
		{Key: "address", Help: addrHelp, Value: h.Address, Show: or(h.Address, "—")},
		{Key: "user", Help: "admin user on the host", Value: h.User, Show: or(h.User, "debian (default)")},
	}
	var domains []keyval.Choice
	var first string
	if c, err := config.Load(); err == nil {
		for i, d := range c.Domains {
			desc := "daily key label " + d.Label
			if i == 0 {
				desc, first = desc+" (default)", d.Name
			}
			domains = append(domains, keyval.Choice{Value: d.Name, Desc: desc})
		}
	}
	out = append(out,
		keyval.Setting{Key: "domain", Help: "which daily key the host trusts", Value: h.Domain, Show: or(h.Domain, first+" (default)"), Choices: domains},
		keyval.Setting{Key: "mode", Help: "what nimble does with the host", Value: h.Mode, Show: or(h.Mode, "managed (default)"), Choices: []keyval.Choice{
			{Value: "managed", Desc: "set up, hardened and kept up to date by nimble"},
			{Value: "ssh", Desc: "only an entry in the ssh config"},
			{Value: "workstation", Desc: "a machine with the config checkout"},
		}})
	if !h.Workstation() {
		out = append(out, keyval.Setting{Key: "jump", Help: "the host it is reached through (ProxyJump)", Value: h.Jump, Show: or(h.Jump, "— directly"),
			Choices: append([]keyval.Choice{{Value: "", Desc: "directly"}}, others...)})
	}
	if h.Managed() {
		reach := strings.Join(h.Reach, ",")
		out = append(out, keyval.Setting{Key: "reach", Help: "hosts it gets ssh entries for (Touch ID through the forwarded agent)", Value: reach,
			Show: or(strings.ReplaceAll(reach, ",", ", "), "—"), List: true,
			Choices: append([]keyval.Choice{{Value: "all", Desc: "every host", Alone: true}}, others...)})
	}
	if h.Managed() || h.Workstation() { // nimble installs nothing on an ssh entry
		os := "debian"
		if h.Workstation() {
			os = ""
		}
		var tools []keyval.Choice
		for _, t := range setup.CatalogFor(os) {
			tools = append(tools, keyval.Choice{Value: t.Name, Desc: t.Short})
		}
		var templates []keyval.Choice
		ts, _ := config.Templates()
		for _, t := range ts {
			templates = append(templates, keyval.Choice{Value: t.Name, Desc: t.Description})
		}
		out = append(out,
			keyval.Setting{Key: "tools", Help: "tools nimble installs", Value: strings.Join(h.Tools, ","),
				Show: or(strings.Join(h.Tools, ", "), "none"), List: true, Choices: tools},
			keyval.Setting{Key: "shell", Help: "login shell", Value: h.Shell, Show: or(h.Shell, "the system's"), Choices: []keyval.Choice{
				{Value: "zsh", Desc: "the Z shell"}, {Value: "bash", Desc: "GNU bash"}, {Value: "", Desc: "the system's login shell"},
			}},
			keyval.Setting{Key: "template", Help: "copy a template in: its tools and shell replace the host's", Show: "(copy one in)", Choices: templates})
	}
	return out
}

// completeSet completes host set: host names, then key=value.
var completeSet = keyval.Complete(hostNames, func(name string) []keyval.Setting {
	h, ok := config.FindHost(name)
	if !ok {
		return nil
	}
	return settings(h)
})

// preview is host name as it would be with the changes: they are written, checked like
// host set checks them and read back, then the file is restored.
func preview(name string, changes []string) (config.Host, error) {
	file := config.HostFile(name)
	before, err := os.ReadFile(file)
	if err != nil {
		return config.Host{}, err
	}
	defer os.WriteFile(file, before, 0o644)
	for _, kv := range changes {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return config.Host{}, errors.New(kv + ": expected key=value")
		}
		if err := config.SetHost(name, k, v); err != nil {
			return config.Host{}, err
		}
	}
	if errs := config.Validate(setup.KnownTools()); len(errs) > 0 {
		return config.Host{}, errs[0]
	}
	h, _ := config.FindHost(name)
	return h, nil
}

// menu is host set's guided way: the changes for host name, or nil.
func menu(name string) ([]string, error) {
	return keyval.Menu(name, func(changes []string) ([]keyval.Setting, error) {
		h, err := preview(name, changes)
		if err != nil {
			return nil, err
		}
		return settings(h), nil
	})
}
