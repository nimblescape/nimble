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
	write("nimble.yaml", "defaultTemplate: dev\ndomains:\n  nimblescape:\n    label: ssh\n  school:\n    label: htl\nlinks: []\n")
	write("templates/dev.yaml", "description: dev host\nos: debian\nshell: zsh\ntools:\n  - starship\n  - go\n")
	write("hosts/swan.yaml", "# swan, the build host\naddress: swan.example.com # public name\nuser: debian\ntemplate: dev\ntools: [docker]\n")
	write("hosts/vm.yaml", "address: vm.example.com\nuser: hannes\ndomain: school\nmode: ssh\n")
	write("toolchain/pins.yaml", "# pins\nnode: 26.8.1\ngo: 1.27.2 # go\npython: 3.12.15\nnpmGlobal:\n  - npm@12.2.1\n  - \"@nimblescape/typescript\"\n")
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
	r, err := Resolve(c, &h)
	if err != nil && !strings.Contains(err.Error(), "is for debian") { // the test may run on macOS
		t.Fatal(err)
	}
	if err == nil && (r.Shell != "zsh" || strings.Join(r.Tools, ",") != "starship,go,docker") {
		t.Fatalf("resolved %+v", r)
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
	for _, want := range []string{`template "nope"`, `unknown tool "warp"`, `mode "weird"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestEditsKeepComments(t *testing.T) {
	dir := repo(t)
	if err := SetHost("swan", "template", "workstation"); err != nil {
		t.Fatal(err)
	}
	if err := SetHost("swan", "tools", "docker,gh"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hosts/swan.yaml"))
	s := string(b)
	for _, want := range []string{"# swan, the build host", "# public name", "template: workstation", "- gh"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
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
	for _, step := range [][4]string{{"go", "", "1.27.2", "1.27.4"}, {"python", "", "3.12.15", "3.12.16"}, {"npmGlobal", "npm", "12.2.1", "12.2.2"}} {
		if err := ReplaceVersion(step[0], step[1], step[2], step[3]); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "toolchain/pins.yaml"))
	s := string(b)
	for _, want := range []string{"go: 1.27.4 # go", "python: 3.12.16", "- npm@12.2.2", "node: 26.8.1", "# pins"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	p, err := LoadPins()
	if err != nil || p.Go != "1.27.4" || p.Python != "3.12.16" {
		t.Fatalf("pins %+v %v", p, err)
	}
}

func TestSetNPMGlobal(t *testing.T) {
	dir := repo(t)
	if err := SetNPMGlobal("@nimblescape/typescript", "0.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := SetNPMGlobal("npm", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "toolchain/pins.yaml"))
	for _, want := range []string{`- "@nimblescape/typescript@0.3.4"`, "  - npm\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in:\n%s", want, b)
		}
	}
	if p, err := LoadPins(); err != nil || p.NPMGlobal[1] != "@nimblescape/typescript@0.3.4" {
		t.Fatalf("%+v %v", p, err)
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

func TestReplaceSpec(t *testing.T) {
	for _, c := range []struct{ line, old, new, want string }{
		{`  - typescript@6.0.3`, "typescript@6.0.3", "typescript@6.0.4", `  - typescript@6.0.4`},
		{`  - "@scope/typescript@6.0.3"`, "typescript@6.0.3", "typescript@6.0.4", `  - "@scope/typescript@6.0.3"`},
		{`  - "@scope/typescript@6.0.3" # x`, "@scope/typescript@6.0.3", "@scope/typescript@6.1.0", `  - "@scope/typescript@6.1.0" # x`},
		{`  - pnpm@9.0.0`, "npm@9.0.0", "npm@9.0.1", `  - pnpm@9.0.0`},
		{`  - npm@9.0.01`, "npm@9.0.0", "npm@9.0.1", `  - npm@9.0.01`},
	} {
		if got := replaceSpec(c.line, c.old, c.new); got != c.want {
			t.Errorf("replaceSpec(%q, %q) = %q, want %q", c.line, c.old, got, c.want)
		}
	}
}
