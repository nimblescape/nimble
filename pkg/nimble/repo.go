package nimble

import "errors"

// Editable refuses config changes on a host: it holds a snapshot of the config, which the
// workstation's next nimble update replaces. Commands that change the config check it
// before they write anything.
func Editable() error {
	if IsSnapshot() {
		return errors.New("this host has a snapshot of the config — change it on the workstation (with --host), then nimble update <host> there")
	}
	return nil
}

// Record commits changes nimble made to the repo (only the given paths). Nothing leaves
// this machine until nimble push publishes the commits.
func Record(message string, paths ...string) error {
	if err := Editable(); err != nil {
		return err
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
	Say("committed: " + message + " (nimble push publishes it)")
	return nil
}
