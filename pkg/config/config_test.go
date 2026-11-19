package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("NIMBLE_HOME", dir)
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nimble.yaml", "format: 2\ndefaultTemplate: dev\ndomains:\n  nimblescape:\n    label: ssh\n  school:\n    label: htl\nlinks: []\n")
	write("templates/dev.yaml", "description: dev host\nos: debian\nshell: zsh\ntools:\n  - starship\n  - go\n")
	write("hosts/swan.yaml", "# swan, the build host\naddress: swan.example.com # public name\nuser: debian\ntools: [starship, go, docker]\nshell: zsh\n")
	write("hosts/vm.yaml", "address: vm.example.com\nuser: hannes\ndomain: school\nmode: ssh\n")
	write("toolchain/pins.yaml", "# pins\nnode: 26.8.1\ngo: 1.27.2 # go\npython: 3.12.15\nrust: 1.100.0\n")
	return dir
}

func TestLoadAndResolve(t *testing.T) {
	repo(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := c.Domain(""); d.Name != "nimblescape" { // first domain is the default
		t.Fatalf("default domain %q", d.Name)
	}
	h, ok := FindHost("swan")
	if !ok || h.Address != "swan.example.com" || !h.Managed() {
		t.Fatalf("swan: %+v", h)
	}
	// a host file holds its tools and shell itself
	r, err := Resolve(c, &h)
	if err != nil || r.Template != "" || r.Shell != "zsh" || strings.Join(r.Tools, ",") != "starship,go,docker" {
		t.Fatalf("resolved %+v %v", r, err)
	}
	// a machine without a host file gets the default template — if it's for this system
	r, err = Resolve(c, nil)
	if err != nil && !strings.Contains(err.Error(), "is for debian") { // the test may run on macOS
		t.Fatal(err)
	}
	if err == nil && (r.Template != "dev" || r.Shell != "zsh" || strings.Join(r.Tools, ",") != "starship,go") {
		t.Fatalf("resolved without a host file %+v", r)
	}
}

func TestMigrateHosts(t *testing.T) {
	dir := repo(t)
	write := func(rel, body string) { os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644) }
	read := func(rel string) string { b, _ := os.ReadFile(filepath.Join(dir, rel)); return string(b) }
	// a repo from before format 2: hosts name their template, or get the default one
	write("nimble.yaml", "# config\ndefaultTemplate: dev\ndomains:\n  nimblescape:\n    label: ssh\n  school:\n    label: htl\nlinks: []\n")
	write("templates/mac.yaml", "os: macos\ntools: [gh]\n")
	write("hosts/swan.yaml", "# swan, the build host\naddress: swan.example.com # public name\ntemplate: dev # the dev setup\ntools: [docker, go]\nharden: sudo\n")
	write("hosts/plain.yaml", "address: plain.example.com\n")
	write("hosts/mac.yaml", "mode: workstation\ntemplate: mac\nshell: bash\n")
	write("hosts/vm.yaml", "address: vm.example.com\nmode: ssh\ntemplate: dev\n")

	changed, migrated, err := MigrateHosts()
	if err != nil || !migrated || strings.Join(changed, ",") != "mac,plain,swan,vm" {
		t.Fatalf("changed %v migrated %v err %v", changed, migrated, err)
	}
	// the template's tools first, then the host's own; the host's shell wins; comments stay
	swan := read("hosts/swan.yaml")
	// tools: is changed in place, shell: takes the template: line's place
	for _, want := range []string{"# swan, the build host", "# public name\nshell: zsh\ntools:\n  - starship\n  - go\n  - docker\nharden: sudo"} {
		if !strings.Contains(swan, want) {
			t.Errorf("swan: missing %q in:\n%s", want, swan)
		}
	}
	for name, want := range map[string]Host{
		"swan":  {Tools: []string{"starship", "go", "docker"}, Shell: "zsh"},
		"plain": {Tools: []string{"starship", "go"}, Shell: "zsh"}, // the default template
		"mac":   {Tools: []string{"gh"}, Shell: "bash"},
		"vm":    {}, // ssh only: the reference just goes
	} {
		h, _ := FindHost(name)
		if h.Template != "" || strings.Join(h.Tools, ",") != strings.Join(want.Tools, ",") || h.Shell != want.Shell {
			t.Errorf("%s: %+v", name, h)
		}
	}
	if c, _ := Load(); c.Format != 2 || !strings.HasPrefix(read("nimble.yaml"), "# config\n") {
		t.Errorf("nimble.yaml:\n%s", read("nimble.yaml"))
	}
	if errs := Validate([]string{"starship", "go", "docker", "gh"}); len(errs) > 0 {
		t.Errorf("migrated repo invalid: %v", errs)
	}
	// once: a host file without tools now means none, not the default template's
	write("hosts/plain.yaml", "address: plain.example.com\n")
	if changed, migrated, err := MigrateHosts(); migrated || len(changed) > 0 || err != nil {
		t.Fatalf("second run: %v %v %v", changed, migrated, err)
	}
	if h, _ := FindHost("plain"); len(h.Tools) > 0 {
		t.Errorf("plain got tools again: %+v", h)
	}
}

func TestStrictAndValidate(t *testing.T) {
	dir := repo(t)
	if errs := Validate([]string{"starship", "go", "docker"}); len(errs) != 0 {
		t.Fatalf("valid repo: %v", errs)
	}
	os.WriteFile(filepath.Join(dir, "templates/bad.yaml"), []byte("tool: [go]\n"), 0o644) // typo: tool
	if _, err := FindTemplate("bad"); err == nil {
		t.Fatal("unknown key accepted")
	}
	os.Remove(filepath.Join(dir, "templates/bad.yaml"))
	os.WriteFile(filepath.Join(dir, "hosts/x.yaml"), []byte("address: x\ntemplate: nope\ntools: [warp]\nmode: weird\n"), 0o644)
	errs := Validate([]string{"starship", "go", "docker"})
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{`template: nope — a host file holds its tools and shell itself`, `unknown tool "warp"`, `mode "weird"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestEditsKeepComments(t *testing.T) {
	dir := repo(t)
	if err := SetHost("swan", "tools", "docker,gh"); err != nil {
		t.Fatal(err)
	}
	if err := SetHost("swan", "shell", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hosts/swan.yaml"))
	s := string(b)
	for _, want := range []string{"# swan, the build host", "# public name", "- gh"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	// template= copies the template's tools and shell over the host's; no reference stays
	if err := SetHost("swan", "template", "dev"); err != nil {
		t.Fatal(err)
	}
	if h, _ := FindHost("swan"); h.Template != "" || strings.Join(h.Tools, ",") != "starship,go" || h.Shell != "zsh" {
		t.Fatalf("after template=dev: %+v", h)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hosts/swan.yaml")); !strings.Contains(string(b), "# public name") {
		t.Errorf("comment lost:\n%s", b)
	}
	if err := SetHost("vm", "template", "dev"); err == nil || !strings.Contains(err.Error(), "ssh only") {
		t.Errorf("template on an ssh-only host: %v", err)
	}
	if err := SetHostServices("swan", []Service{{Service: "claude", Repo: "a/b", State: "enabled"}}); err != nil {
		t.Fatal(err)
	}
	h, _ := FindHost("swan")
	if len(h.Services) != 1 || h.Services[0].Repo != "a/b" {
		t.Fatalf("services %+v", h.Services)
	}
	if err := SetTemplate("dev", "tools", "starship,rust"); err != nil {
		t.Fatal(err)
	}
	if tm, _ := FindTemplate("dev"); strings.Join(tm.Tools, ",") != "starship,rust" || tm.Shell != "zsh" {
		t.Fatalf("template %+v", tm)
	}
	if err := AddDomain("lab", "lab"); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(); len(c.Domains) != 3 || c.Domains[2].Name != "lab" {
		t.Fatalf("domains %+v", c.Domains)
	}
}

func TestReplaceVersion(t *testing.T) {
	dir := repo(t)
	for _, step := range [][3]string{{"go", "1.27.2", "1.27.4"}, {"python", "3.12.15", "3.12.16"}, {"rust", "1.100.0", "1.101.0"}} {
		if err := ReplaceVersion(step[0], step[1], step[2]); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "toolchain/pins.yaml"))
	s := string(b)
	for _, want := range []string{"go: 1.27.4 # go", "python: 3.12.16", "rust: 1.101.0", "node: 26.8.1", "# pins"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	p, err := LoadPins()
	if err != nil || p.Go != "1.27.4" || p.Python != "3.12.16" {
		t.Fatalf("pins %+v %v", p, err)
	}
	if err := ReplaceVersion("node", "26.8.0", "26.8.2"); err == nil { // 26.8.0 is in no line
		t.Error("a version that isn't there was replaced")
	}
}

func TestReplaceToken(t *testing.T) {
	for _, c := range []struct{ line, old, new, want string }{
		{"go: 1.27.2 # go", "1.27.2", "1.27.4", "go: 1.27.4 # go"},
		{"go: 1.27.21", "1.27.2", "1.27.4", "go: 1.27.21"},         // part of a longer version
		{"rust: 11.100.0", "1.100.0", "1.101.0", "rust: 11.100.0"}, // the same
	} {
		if got := replaceToken(c.line, c.old, c.new); got != c.want {
			t.Errorf("replaceToken(%q, %q) = %q, want %q", c.line, c.old, got, c.want)
		}
	}
}

func TestValidateCatches(t *testing.T) {
	dir := repo(t)
	has := func(want string) bool {
		for _, e := range Validate([]string{"go", "docker", "gh", "node"}) {
			if strings.Contains(e.Error(), want) {
				return true
			}
		}
		return false
	}
	write := func(rel, body string) { os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644) }
	pins, _ := os.ReadFile(filepath.Join(dir, "toolchain/pins.yaml"))

	write("toolchain/pins.yaml", "nod: 26.8.1\n") // a typo: push must not publish it
	if !has("pins.yaml") {
		t.Error("an unknown key in pins.yaml isn't reported")
	}
	write("toolchain/pins.yaml", "go: 1.27\n")
	if !has("go \"1.27\"") {
		t.Error("a pin that isn't x.y.z isn't reported")
	}
	write("toolchain/pins.yaml", string(pins))

	write("hosts/bad.yaml", "address: \"a b; rm -rf ~\"\n")
	if !has("hosts/bad.yaml: address") {
		t.Error("an address with shell characters isn't reported")
	}
	os.Remove(filepath.Join(dir, "hosts/bad.yaml"))

	if ValidLabel("my-lab") == nil || ValidLabel("") == nil || ValidLabel("lab_2") != nil {
		t.Error("ValidLabel")
	}
}
