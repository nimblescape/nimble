package hosts

import (
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
)

// Two kinds of ssh config come from hosts/:
//
//   - the workstation's: an entry for every host, each with the daily key of its domain
//     (Touch ID). It stays on the workstation.
//   - a host's own, for the hosts its reach names: no keys on the host — it logs in with the
//     workstation's key through the agent forwarded to it, so Touch ID confirms there too.
//     The workstation renders it into that host's snapshot only.
//
// A host with jump is reached through that host (ProxyJump) in both.

// SnapshotSSHConfig is where a host's own ssh entries are in its snapshot.
const SnapshotSSHConfig = "ssh/config"

// SSHConfig renders the workstation's ssh client entries for all hosts: each uses the daily
// key of its domain; managed hosts forward the agent (sudo there asks for Touch ID).
func SSHConfig(c *config.Config, handle func(label string) string) string {
	hs, _ := config.Hosts()
	var b strings.Builder
	for _, h := range hs {
		if !connectable(h) {
			continue
		}
		var ids []string
		if d, err := c.Domain(h.Domain); err == nil {
			ids = append(ids, nimble.Tilde(handle(d.Label)))
			if old := handle(d.Label) + ".old"; nimble.Exists(old) { // a renewed daily key: until this host has the new one
				ids = append(ids, nimble.Tilde(old))
			}
		}
		b.WriteString(entry(h, "", ids, h.Managed()))
	}
	return b.String()
}

// HostSSHConfig renders host from's own ssh entries: the hosts its reach names ("all":
// every host but itself) and the hosts they are reached through. "" when it reaches none.
func HostSSHConfig(from config.Host, hs []config.Host) string {
	byName := map[string]config.Host{}
	for _, h := range hs {
		byName[h.Name] = h
	}
	want := map[string]bool{}
	var add func(name string)
	add = func(name string) {
		h, ok := byName[name]
		if !ok || name == from.Name || want[name] || !connectable(h) {
			return
		}
		want[name] = true
		add(h.Jump) // through a host other than from itself: that one needs an entry too
	}
	for _, r := range from.Reach {
		if r == "all" {
			for _, h := range hs {
				add(h.Name)
			}
		} else {
			add(r)
		}
	}
	var b strings.Builder
	for _, h := range hs { // in hosts/ order: sorted by name
		if want[h.Name] {
			b.WriteString(entry(h, from.Name, nil, false))
		}
	}
	return b.String()
}

// connectable: a host ssh connects to (a workstation isn't one, nor a host without address).
func connectable(h config.Host) bool { return !h.Workstation() && h.Address != "" }

// entry is one Host block. from is where ssh runs ("" for the workstation): a host reached
// through from itself is connected to directly. Managed hosts get the agent forwarded, so
// sudo there is confirmed with the workstation's key; addKeys adds a key to the agent the
// first time it is used (only where the keys are).
func entry(h config.Host, from string, ids []string, addKeys bool) string {
	var b strings.Builder
	b.WriteString("Host " + h.Name + "\n  HostName " + h.Address + "\n  User " + h.AdminUser() + "\n")
	if h.Jump != "" && h.Jump != from {
		b.WriteString("  ProxyJump " + h.Jump + "\n")
	}
	for _, id := range ids {
		b.WriteString("  IdentityFile " + id + "\n")
	}
	if h.Managed() {
		b.WriteString("  ForwardAgent yes\n")
		if addKeys {
			b.WriteString("  AddKeysToAgent yes\n")
		}
	}
	return b.String() + "\n"
}
