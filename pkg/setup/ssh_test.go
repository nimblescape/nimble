package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// On a host, apply installs the ssh entries from its snapshot and includes them at the top
// of ~/.ssh/config, keeping the user's own lines; without them (reach removed) it takes out
// what it wrote.
func TestHostSSHConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, ".nimble")
	t.Setenv("NIMBLE_HOME", repo)
	w := func(p, body string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	cfg := filepath.Join(home, ".ssh", "config")
	gen := filepath.Join(repo, "cache", "ssh_config")

	// not a snapshot (a workstation, or no host file): nothing
	if err := HostSSHConfig(); err != nil || read(gen) != "" {
		t.Fatalf("outside a snapshot: %v %q", err, read(gen))
	}

	w(filepath.Join(repo, ".snapshot"), "host: swan\n")
	w(filepath.Join(repo, "ssh", "config"), "Host grizzly\n  HostName 10.0.20.5\n")
	w(cfg, "Host own\n  HostName own.example.com\n")
	if err := HostSSHConfig(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(gen), "Host grizzly") {
		t.Errorf("generated: %q", read(gen))
	}
	aliases := filepath.Join(repo, "cache", AliasesFile)
	if !strings.Contains(read(aliases), "alias grizzly='ssh grizzly'") {
		t.Errorf("aliases: %q", read(aliases))
	}
	if c := read(cfg); !strings.HasPrefix(c, "Include ~/.nimble/cache/ssh_config\n") || !strings.Contains(c, "Host own\n") {
		t.Errorf("~/.ssh/config: %q", c)
	}

	// again: nothing changes
	before := read(cfg)
	if err := HostSSHConfig(); err != nil || read(cfg) != before {
		t.Errorf("second run changed ~/.ssh/config: %q", read(cfg))
	}

	// reach removed: the generated file and the Include go, the user's lines stay
	os.RemoveAll(filepath.Join(repo, "ssh"))
	if err := HostSSHConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gen); err == nil {
		t.Error("generated file still there")
	}
	if a := read(aliases); strings.Contains(a, "grizzly") || !strings.Contains(a, "nimble()") {
		t.Errorf("alias file after: %q", a) // the hosts' aliases go, the nimble function stays
	}
	if c := read(cfg); strings.Contains(c, "Include") || c != "Host own\n  HostName own.example.com\n" {
		t.Errorf("~/.ssh/config after: %q", c)
	}

	// a ~/.ssh/config that held only the Include goes with it
	w(filepath.Join(repo, "ssh", "config"), "Host grizzly\n  HostName 10.0.20.5\n")
	os.Remove(cfg)
	if err := HostSSHConfig(); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(repo, "ssh"))
	if err := HostSSHConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Errorf("~/.ssh/config left with %q", read(cfg))
	}
}

// The alias file, loaded by bash and zsh in an interactive shell: directory aliases, an
// alias per host unless the name means something already (a command, your alias, a
// directory alias), and the nimble function that runs what nimble leaves for the shell.
// A non-interactive shell gets none of it.
func TestAliasesScript(t *testing.T) {
	gen := "IgnoreUnknown UseKeychain\n\nHost grizzly\n  HostName 10.0.20.5\n  ProxyJump swan\n\nHost swan\n  HostName s\n\n" +
		"Host own\n  HostName o\n\nHost true\n  HostName t\n\nHost *\n  ControlMaster auto\n"
	got := aliasesScript(map[string]string{"1a1": "~/work/1a", "swan": "~/s", "abs": "/tmp/a b"}, gen)
	for _, w := range []string{"alias grizzly='ssh grizzly'", `alias 1a1='cd "$HOME/work/1a"'`, `alias abs='cd "/tmp/a b"'`, "nimble() {"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if strings.Contains(got, "'ssh *'") {
		t.Errorf("a pattern got an alias:\n%s", got)
	}
	tmp := t.TempDir()
	file := filepath.Join(tmp, AliasesFile)
	if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home) // no rc files of the machine running the test
	os.MkdirAll(filepath.Join(home, "work", "1a"), 0o755)
	// a stand-in nimble: it leaves an alias for the shell and fails, as a command may
	bin := filepath.Join(tmp, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "nimble"), []byte("#!/bin/sh\necho \"alias zz='echo from-next'\" > \"$NIMBLE_NEXT\"\nexit 3\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TMPDIR", tmp)

	// own is an alias of yours already, true a command, swan a directory alias: the host
	// aliases step back
	// eval: zsh -c parses the whole string first, a prompt line by line (an alias defined in
	// one line works in the next)
	script := "alias own='echo mine'\n. \"" + file + "\"\nalias grizzly; alias swan; alias own; alias true || echo 'no alias true'\n" +
		"eval 1a1\npwd\nnimble\necho rc=$?\neval zz\nls \"$TMPDIR\" | grep -c nimble-next || true\n"
	shells := map[string][]string{"bash": {"--norc", "--noprofile", "-i", "-c"}, "zsh": {"-f", "-i", "-c"}}
	for sh, args := range shells {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		out, _ := exec.Command(sh, append(args, script)...).Output()
		s := string(out)
		for _, w := range []string{"ssh grizzly", `cd "$HOME/s"`, "echo mine", "no alias true", filepath.Join("work", "1a") + "\n",
			"rc=3", "from-next", "\n0\n"} {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q in:\n%s", sh, w, s)
			}
		}
		if strings.Contains(s, "ssh swan") || strings.Contains(s, "ssh own") {
			t.Errorf("%s: a host alias took a name:\n%s", sh, s)
		}
		// not interactive (ssh host cmd, scripts): nothing
		out, _ = exec.Command(sh, "-c", `. "`+file+`"; alias grizzly || echo none`).Output()
		if !strings.Contains(string(out), "none") {
			t.Errorf("%s, not interactive: %s", sh, out)
		}
		// turned off
		out, _ = exec.Command(sh, append(args, `NIMBLE_NO_SSH_ALIASES=1; . "`+file+`"; alias grizzly || echo none`)...).Output()
		if !strings.Contains(string(out), "none") {
			t.Errorf("%s, turned off: %s", sh, out)
		}
	}
}
