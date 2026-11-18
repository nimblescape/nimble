package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nimblescape/nimble/pkg/config"
)

func TestMerged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NIMBLE_HOME", dir)
	files := map[string]string{
		"nimble.yaml":         "defaultTemplate: base\ndomains:\n  nimblescape:\n    label: ssh\n  school:\n    label: htl\nlinks: []\n",
		"templates/base.yaml": "description: nothing\n",
		"templates/dev.yaml":  "description: dev host\nos: debian\nshell: zsh\ntools: [starship, go]\n",
		"hosts/swan.yaml":     "# swan, the build host\n# second line\naddress: swan.example.com # public name\nuser: admin\ntemplate: dev\ntools: [docker, go]\nharden: sudo\nservices:\n  - {service: claude, repo: owner/app, state: enabled}\n",
		"hosts/plain.yaml":    "address: plain.example.com\n",
		"hosts/vm.yaml":       "address: vm.example.com\ndomain: school\nmode: ssh\n",
		"hosts/mac.yaml":      "# This Mac.\nmode: workstation\ntools: [gh]\nshell: zsh\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	show := func(name string) string {
		h, ok := config.FindHost(name)
		if !ok {
			t.Fatalf("no host %s", name)
		}
		out, err := merged(c, h, headComments(files["hosts/"+name+".yaml"]))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	expect := func(out string, want ...string) {
		t.Helper()
		lines := map[string]bool{}
		for _, l := range strings.Split(out, "\n") {
			lines[strings.Join(strings.Fields(l), " ")] = true
		}
		for _, w := range want {
			if !lines[w] {
				t.Errorf("missing line %q in:\n%s", w, out)
			}
		}
	}

	// own settings bare, template values marked, a host tool the template has too counted once
	swan := show("swan")
	expect(swan,
		"# swan, the build host", "# second line",
		"address: swan.example.com", "user: admin", "domain: nimblescape # default: the first in nimble.yaml",
		"mode: managed # default", "template: dev",
		"tools:", "- starship # template dev", "- go # template dev", "- docker",
		"shell: zsh # template dev", "harden: sudo",
		"- {service: claude, repo: owner/app, state: enabled}")
	if strings.Count(swan, "- go") != 1 {
		t.Errorf("go listed twice:\n%s", swan)
	}

	// nothing set but the address: every other value is a default
	expect(show("plain"),
		"user: debian # default", "template: base # default: defaultTemplate in nimble.yaml",
		"tools: [] # template base has none", "harden: full # default")
	if out := show("plain"); !strings.Contains(out, "# shell: the system's default login shell") || strings.HasPrefix(out, "#") {
		t.Errorf("plain:\n%s", out)
	}

	// a workstation: no admin user, domain or hardening defaults
	mac := show("mac")
	expect(mac, "# This Mac.", "mode: workstation", "template: base # default: defaultTemplate in nimble.yaml",
		"tools:", "- gh", "shell: zsh")
	for _, no := range []string{"user:", "domain:", "harden:"} {
		if strings.Contains(mac, no) {
			t.Errorf("workstation shows %s:\n%s", no, mac)
		}
	}

	// ssh only: no template, tools or hardening
	vm := show("vm")
	expect(vm, "domain: school", "mode: ssh")
	for _, no := range []string{"template", "tools", "harden"} {
		if strings.Contains(vm, no+":") {
			t.Errorf("vm shows %s:\n%s", no, vm)
		}
	}
}
