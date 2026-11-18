package nimble

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// A host holds a snapshot of the config. Its own host file (tools, services, shell …) can be
// changed there with nimble's commands; everything else changes on the workstation. The
// snapshot keeps the host file as it was sent (SentDir), and every change made on the host
// adds its message to ChangeLog. The workstation's next nimble update <host> reads both
// back, merges the host file into the config repo and commits it, then sends a new snapshot
// (which starts both afresh).
const (
	SentDir   = ".sent"
	ChangeLog = ".changed"
)

// Editable refuses config changes on a host: it holds a snapshot of the config, which the
// workstation's next nimble update replaces. Commands that change the config check it
// before they write anything; the host's own file is checked with EditableHost.
func Editable() error {
	if IsSnapshot() {
		h := SnapshotHost()
		return errors.New("this host has a snapshot of the config: here only its own host file changes (tool select, service, host set|edit " + h +
			") — change the rest on the workstation, then nimble update " + h + " there")
	}
	return nil
}

// EditableHost refuses changing host name's file on a host, unless it is the host's own.
func EditableHost(name string) error {
	if IsSnapshot() && name != SnapshotHost() {
		return Editable()
	}
	return nil
}

// HostChanges reports what was changed on this host and isn't in the config repo yet:
// whether its host file differs from the one sent, and the changes recorded here (none
// for a hand edit). Off a host it reports nothing.
func HostChanges() (changed bool, log []string) {
	name := SnapshotHost()
	if !IsSnapshot() || name == "" {
		return false, nil
	}
	sent, err := os.ReadFile(RepoFile(SentDir, "hosts", name+".yaml"))
	if err != nil {
		return false, nil
	}
	now, err := os.ReadFile(RepoFile("hosts", name+".yaml"))
	if err != nil || string(now) == string(sent) {
		return false, nil
	}
	return true, Lines(ReadTrim(RepoFile(ChangeLog)))
}

// Record commits changes nimble made to the repo (only the given paths). Nothing leaves
// this machine until nimble push publishes the commits. On a host it notes the change for
// the workstation instead.
func Record(message string, paths ...string) error {
	if IsSnapshot() {
		return noteHostChange(message, paths)
	}
	if !IsCheckout() {
		return nil
	}
	if err := (Cmd{Name: "git", Args: append([]string{"-C", Repo(), "add", "--all", "--"}, paths...)}).Do(); err != nil {
		return err
	}
	if Git(append([]string{"diff", "--cached", "--name-only", "--"}, paths...)...) == "" {
		return nil // nothing changed
	}
	if err := (Cmd{Name: "git", Quiet: true, Args: append([]string{"-C", Repo(), "commit", "-m", message, "--"}, paths...)}).Do(); err != nil {
		return err
	}
	subject, _, _ := strings.Cut(message, "\n")
	Say("committed: " + subject + " (nimble push publishes it)")
	return nil
}

// noteHostChange adds a change made on this host to ChangeLog — only its own host file
// can change here.
func noteHostChange(message string, paths []string) error {
	name := SnapshotHost()
	for _, p := range paths {
		if p != "hosts" && p != "hosts/"+name+".yaml" {
			return Editable()
		}
	}
	f, err := os.OpenFile(RepoFile(ChangeLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, message)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	Say("recorded here: " + message + " (the workstation's next nimble update " + name + " takes it into the config repo)")
	return nil
}
