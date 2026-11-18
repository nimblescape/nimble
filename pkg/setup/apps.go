package setup

import (
	"os/exec"

	"github.com/nimblescape/nimble/pkg/nimble"
)

// itermSettings points iTerm2 at the settings kept in the personal layer
// (personal/macos/iterm2/com.googlecode.iterm2.plist): iTerm2 loads them at its next
// start, on this Mac or a new one, and can save changes back there (Settings → General →
// Settings → Save changes), so nimble push shares them.
func itermSettings() {
	dir := nimble.RepoFile("personal", "macos", "iterm2")
	if !nimble.Exists(dir + "/com.googlecode.iterm2.plist") {
		return
	}
	const domain = "com.googlecode.iterm2"
	if nimble.Output("defaults", "read", domain, "PrefsCustomFolder") == dir &&
		nimble.Output("defaults", "read", domain, "LoadPrefsFromCustomFolder") == "1" {
		return
	}
	_ = exec.Command("defaults", "write", domain, "PrefsCustomFolder", "-string", dir).Run()
	_ = exec.Command("defaults", "write", domain, "LoadPrefsFromCustomFolder", "-bool", "true").Run()
	nimble.Say("iTerm2 loads its settings from " + nimble.Tilde(dir) + " (from its next start)")
	// iTerm2 keeps this choice to itself (not in its preferences), so nimble can't set it
	nimble.Say("note: to save changes back there, set iTerm2's Settings → General → Settings → Save changes: Automatically (once on each Mac)")
}
