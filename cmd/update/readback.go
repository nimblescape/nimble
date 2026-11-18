package update

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nimblescape/nimble/cmd/push"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/remote"
	"github.com/nimblescape/nimble/pkg/setup"
)

// A host can change its own host file (tool select, service, host set|edit there, or by
// hand); its snapshot keeps the file as it was sent. Before a host gets a new snapshot, the
// workstation reads its changes back into the config repo, so nothing done there is lost.

// SendConfig reads back what was changed on host h, then sends it its new snapshot.
func SendConfig(h string) error {
	if err := ReadBack(h); err != nil {
		return err
	}
	return nimble.Wait("send its config snapshot", func() error { return remote.SendSnapshot(h) })
}

// ReadBack takes what was changed on host h since its last snapshot into the config repo:
// the host file as sent, as it is on the host and as it is here are merged three ways, a
// clash is resolved in the editor (like a pull), and the result is checked and committed
// (nimble push publishes it). Without changes on the host it does nothing.
func ReadBack(h string) error {
	if !nimble.IsCheckout() {
		return errors.New("reading back a host's changes needs the config checkout — run this on a workstation")
	}
	var sent, now []byte
	var log []string
	err := nimble.Wait("look for changes made on "+h, func() (err error) {
		sent, now, log, err = remote.FetchChanges(h)
		return err
	})
	if err != nil {
		return err
	}
	if sent == nil || bytes.Equal(sent, now) {
		return nil
	}
	return mergeBack(h, sent, now, log)
}

// resolve opens files with conflicts in the editor until they are resolved; interactive
// says whether it can (tests replace both).
var (
	resolve     = push.Resolve
	interactive = nimble.Interactive
)

// mergeBack merges host h's file as changed there (now; sent is the base) into the config
// repo and commits it. When the merge clashes and can't be resolved, the file here stays as
// it was and the error keeps the host from getting a new snapshot: its change stays there.
func mergeBack(h string, sent, now []byte, log []string) error {
	rel := filepath.Join("hosts", h+".yaml")
	file := config.HostFile(h)
	here, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("%s was changed on %s, but it is gone here: %w", rel, h, err)
	}
	nimble.Step(rel + " was changed on " + h + " — merge it into the config repo")
	for _, l := range log {
		fmt.Println("  " + l)
	}
	tmp, err := os.MkdirTemp("", "nimble-readback")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	base, theirs := filepath.Join(tmp, "sent"), filepath.Join(tmp, "host")
	if err := os.WriteFile(base, sent, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(theirs, now, 0o644); err != nil {
		return err
	}
	undo := func() { _ = os.WriteFile(file, here, 0o644) }

	conflicts := 0
	merge := exec.Command("git", "merge-file", "-L", "here", "-L", "as sent to "+h, "-L", "changed on "+h, file, base, theirs)
	var exit *exec.ExitError
	switch err := merge.Run(); {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 128: // the number of conflicts
		conflicts = exit.ExitCode()
	default:
		undo()
		return fmt.Errorf("merge %s: %w", rel, err)
	}
	var problems []string
	if conflicts == 0 {
		for _, e := range config.Validate(setup.KnownTools()) {
			if strings.Contains(e.Error(), rel) {
				problems = append(problems, e.Error())
			}
		}
	}
	if conflicts > 0 || len(problems) > 0 {
		if conflicts > 0 {
			nimble.Warn(rel + " was changed here and on " + h + " in the same place")
		}
		for _, p := range problems {
			nimble.Warn(p)
		}
		if !interactive() {
			undo()
			return errors.New(rel + " not merged — " + h + " keeps its change: nimble update " + h + " on a terminal resolves it")
		}
		if err := resolve(nimble.Repo(), []string{rel}); err != nil {
			undo()
			if errors.Is(err, nimble.ErrCancelled) {
				nimble.Say("stopped — " + rel + " is as it was here, and " + h + " keeps its change: nimble update " + h + " merges it again")
			}
			return err
		}
	}
	return nimble.Record(readBackMessage(h, log), rel)
}

// readBackMessage is the commit message for host h's file changed there: the change
// recorded there, or what they were; a hand edit records none.
func readBackMessage(h string, log []string) string {
	switch len(log) {
	case 0:
		return "hosts: " + h + " changed on " + h
	case 1:
		return log[0] + " (on " + h + ")"
	}
	return fmt.Sprintf("hosts: %s changed on %s (%d changes)\n\n%s", h, h, len(log), strings.Join(log, "\n"))
}
