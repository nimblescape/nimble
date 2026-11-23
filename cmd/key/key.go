package key

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdKey() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key <command>",
		Short: "The hardware keys in use: daily keys on workstations, YubiKey backups",
		Long: heredoc.Doc(`
			keys/ is the inventory of every hardware key in use: keys/enclave/<machine>-<label>.pub
			for a workstation's daily key (Secure Enclave, Touch ID), keys/yubikey/<name>.pub for
			a backup in a safe. A host trusts its domain's daily keys and every backup; after a
			change, nimble update all adds new keys there and revokes retired ones.
		`),
	}
	cmd.AddCommand(newList(), newEnclave(), newYubikey(), newRemove())
	return cmd
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validity(e keys.Enclave) string {
	switch {
	case !e.Valid:
		return nimble.Red("expired / not valid")
	case e.ValidTo.IsZero():
		return "valid"
	case time.Until(e.ValidTo) < 30*24*time.Hour:
		return nimble.Yellow("valid until " + e.ValidTo.Format("2006-01-02") + " — renew: nimble key enclave")
	default:
		return "valid until " + e.ValidTo.Format("2006-01-02")
	}
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "Show the keys by domain, and this Mac's Secure Enclave keys", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			local := map[string]bool{}
			if runtime.GOOS == "darwin" {
				for _, d := range c.Domains {
					if p := keys.Parse(nimble.ReadTrim(keys.HandlePath(d.Label) + ".pub")); p.Key != "" {
						local[p.ID()] = true
					}
				}
			}
			mark := func(k keys.Public) string {
				if local[k.ID()] {
					return nimble.Green("  ← this Mac")
				}
				return ""
			}
			for _, d := range c.Domains {
				nimble.Header("Domain " + d.Name + " — daily key label " + d.Label)
				for _, k := range keys.Repo() {
					if k.Kind == "enclave" && k.Label == d.Label {
						fmt.Printf("  %-36s %s%s\n", "enclave/"+k.Name, k.Type, mark(k))
					}
				}
			}
			nimble.Header("YubiKey backups — trusted in every domain")
			for _, k := range keys.Repo() {
				if k.Kind == "yubikey" {
					fmt.Printf("  %-36s %s%s\n", "yubikey/"+k.Name, k.Type, mark(k))
				}
			}
			if runtime.GOOS == "darwin" {
				nimble.Header("This Mac's Secure Enclave")
				role := map[string]string{} // fingerprint → how nimble uses the key
				for _, d := range c.Domains {
					h := keys.HandlePath(d.Label)
					role[keys.Fingerprint(nimble.ReadTrim(h+".pub"))] = nimble.Green("  ← in use (" + nimble.Tilde(h) + ")")
					if fp := keys.Fingerprint(nimble.ReadTrim(h + ".old.pub")); fp != "" {
						role[fp] = nimble.Yellow("  ← the old key, kept for the handover")
					}
				}
				for _, e := range keys.Enclaves() {
					fmt.Printf("  %-16s protection %-5s %s%s\n", e.Label, e.Protection, validity(e), role[e.Fingerprint])
				}
			}
			if hs := keys.Handovers(); len(hs) > 0 {
				nimble.Header("Handovers — renewed keys, the old one still kept")
				for _, h := range hs {
					fmt.Printf("  %-36s renewed %s\n", h.Key, h.Since.Format("2006-01-02"))
					line := "    " + h.Describe(c)
					if h.Overdue() && len(h.Waiting(c)) > 0 {
						line = nimble.Yellow(line)
					}
					fmt.Println(line)
				}
			}
			return nil
		},
	}
}

func newEnclave() *cobra.Command {
	var dropOld, renew bool
	var handover string
	cmd := &cobra.Command{
		Use:   "enclave [domain]",
		Short: "Create or renew this workstation's daily key for a domain",
		Long: heredoc.Doc(`
			Make sure this Mac has the domain's daily key in its Secure Enclave (Touch ID): create
			one if there is none, renew it when it is invalid or about to expire — or now, with
			--renew. Then save its key handle in ~/.ssh, add its public key to keys/enclave/ and
			commit.

			A renewal hands over from the old key to the new one. The new key is the primary one
			at once: it replaces the old one in keys/enclave/ and ssh offers it first, so every
			host trusts it from its next nimble update. The old key stays on this Mac for the
			handover time (--handover, 14 days by default, at most until it expires): its handle
			is ~/.ssh/id_ecdsa_sk_rk_<label>.old and ssh offers it next, so the hosts that
			weren't updated yet stay reachable. nimble update and init note each host that has
			the new key; once every managed host of the domain has it, the next nimble update
			removes the old key from this Mac by itself. nimble key list shows how far it is.
			--drop-old removes it earlier, after a warning naming the hosts that would then
			need a YubiKey.

			On a new Mac a YubiKey backup gets you onto the hosts for that first nimble update all
			(nimble update all --yubikey). Without a domain: the default domain (the first in
			nimble.yaml), which every host without a domain: line belongs to.
		`),
		Example: heredoc.Doc(`
			nimble key enclave                          # the default domain
			nimble key enclave lab                      # the domain lab
			nimble key enclave --renew --handover 7d    # a new key now; the old one for a week at most
			nimble key enclave --drop-old               # end a handover early
		`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				return errors.New("daily keys in a TPM on Debian workstations come later — use a YubiKey meanwhile (nimble key yubikey)")
			}
			c, err := config.Load()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			d, err := c.Domain(name)
			if err != nil {
				return err
			}
			label := d.Label
			handle := keys.HandlePath(label)
			machine := hosts.MachineName() + "-" + label
			if dropOld {
				return dropOldKey(c, handle, "enclave/"+machine)
			}
			span, err := keys.ParseHandover(handover)
			if err != nil {
				return err
			}
			cur, ok := keys.DailyKey(handle)
			switch {
			case ok && !cur.ExpiresSoon() && !renew:
				nimble.Step("Secure Enclave key " + cur.Label + " — " + validity(cur))
			case ok: // renew: a new key next to the old one, which stays until the hosts have the new one
				if nimble.Exists(handle + ".old.pub") {
					return errors.New("a handover from an earlier renewal is still running (" + nimble.Tilde(handle+".old") + ") — let it end, or end it: nimble key enclave --drop-old")
				}
				if renew {
					nimble.Step("renewing the daily key " + cur.Label + " (" + validity(cur) + ")")
				} else {
					nimble.Step("the daily key " + cur.Label + " is " + validity(cur) + " — renewing it")
				}
				e, err := createEnclave(label)
				if err != nil {
					return err
				}
				if err := exportHandle(e, handle+".new"); err != nil { // first the new handle, then the switch
					return err
				}
				for _, suffix := range []string{"", ".pub"} { // the old handle stays as .old: ssh keeps offering it
					if err := os.Rename(handle+suffix, handle+".old"+suffix); err != nil {
						return err
					}
					if err := os.Rename(handle+".new"+suffix, handle+suffix); err != nil {
						return err
					}
				}
				if err := os.WriteFile(nimble.RepoFile("keys", "enclave", machine+".pub"), []byte(pubLine(handle, machine)), 0o644); err != nil {
					return err
				}
				sshConfig(c)
				until := time.Now().Add(span)
				if !cur.ValidTo.IsZero() && cur.ValidTo.Before(until) {
					until = cur.ValidTo // the old key can't hand over longer than it lives
				}
				h := keys.Handover{Key: "enclave/" + machine, Old: cur.Fingerprint, Since: time.Now(), Until: until}
				if err := keys.StartHandover(h); err != nil {
					return err
				}
				if err := nimble.Record("keys: renew enclave/"+machine, "keys"); err != nil {
					return err
				}
				nimble.Say("the new key " + e.Label + " is the daily key now; the old one " + cur.Label + " stays on this Mac until every host has the new one, at most until " + until.Format("2006-01-02"))
				nimble.Step("roll it out: nimble update all — the old key opens the hosts until they have the new one; then nimble removes it")
				return nil
			default: // no handle here, or its key is gone: use a valid key with this label, else create one
				e, found := keys.FindEnclave(label)
				if !found || e.ExpiresSoon() {
					if e, err = createEnclave(label); err != nil {
						return err
					}
				} else {
					nimble.Step("Secure Enclave key " + e.Label + " — " + validity(e))
				}
				if err := exportHandle(e, handle); err != nil {
					return err
				}
			}
			added, err := addToRepo(handle+".pub", "enclave", machine)
			if err != nil {
				return err
			}
			if !added {
				return nil
			}
			if err := nimble.Record("keys: add enclave/"+machine, "keys"); err != nil {
				return err
			}
			nimble.Step("roll it out: nimble update all")
			return nil
		},
	}
	cmd.Flags().BoolVar(&renew, "renew", false, "create a new key now, even if the current one is still valid")
	cmd.Flags().StringVar(&handover, "handover", "14d", "how long a renewal keeps the old key at most (e.g. 7d, 2w)")
	cmd.Flags().BoolVar(&dropOld, "drop-old", false, "end a handover now: remove the old key from this Mac")
	return cmd
}

// createEnclave creates a Secure Enclave key (Touch ID for every use). Labels aren't unique
// in the enclave and a second key with the same label can't be told apart when its handle
// is exported, so a renewal gets a label of its own: <label>-<date>.
func createEnclave(label string) (keys.Enclave, error) {
	taken := map[string]bool{}
	for _, e := range keys.Enclaves() {
		taken[e.Label] = true
	}
	name := label
	if taken[name] {
		name = label + "-" + time.Now().Format("20060102")
	}
	if taken[name] {
		name = label + "-" + time.Now().Format("20060102-150405")
	}
	nimble.Step("create Secure Enclave key " + name + " (Touch ID)")
	if err := nimble.Run("sc_auth", "create-ctk-identity", "-l", name, "-k", "p-256-ne", "-t", "bio"); err != nil {
		return keys.Enclave{}, err
	}
	e, ok := keys.FindEnclave(name)
	if !ok || e.Fingerprint == "" {
		return e, errors.New("the Secure Enclave doesn't list the new key " + name + " (sc_auth list-ctk-identities)")
	}
	return e, nil
}

// exportHandle writes the key handle of a Secure Enclave key to handle and handle.pub (the
// private key stays in the enclave). It asks nothing: the keychain has no PIN, and files
// in the temporary folder may be overwritten.
func exportHandle(e keys.Enclave, handle string) error {
	if keys.Fingerprint(nimble.ReadTrim(handle+".pub")) == e.Fingerprint && nimble.Exists(handle) {
		return nil
	}
	tmp, err := os.MkdirTemp("", "nimble-enclave")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	nimble.Step("save the key handle to " + nimble.Tilde(handle))
	c := exec.Command("ssh-keygen", "-w", keys.SKProvider, "-K", "-N", "")
	c.Dir = tmp
	c.Env = append(os.Environ(), "SSH_ASKPASS=/usr/bin/true", "SSH_ASKPASS_REQUIRE=force")
	c.Stdin = strings.NewReader(strings.Repeat("y\n", 64)) // "overwrite?" for keys that share a label
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh-keygen -K: %w: %s", err, strings.TrimSpace(string(out)))
	}
	src := filepath.Join(tmp, "id_ecdsa_sk_rk_"+e.Label)
	if keys.Fingerprint(nimble.ReadTrim(src+".pub")) != e.Fingerprint {
		return fmt.Errorf("the Secure Enclave didn't return the handle of key %s (%s) — do two keys share that label? sc_auth list-ctk-identities", e.Label, e.Fingerprint)
	}
	for _, suffix := range []string{"", ".pub"} {
		data, err := os.ReadFile(src + suffix)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if suffix == ".pub" {
			mode = 0o644
		}
		_ = os.Remove(handle + suffix)
		if err := os.WriteFile(handle+suffix, data, mode); err != nil {
			return err
		}
	}
	return nil
}

// pubLine is the line for keys/: the handle's public key with name as its comment.
func pubLine(handle, name string) string {
	p := keys.Parse(nimble.ReadTrim(handle + ".pub"))
	return p.Type + " " + p.Key + " " + name + "\n"
}

// sshConfig regenerates the ssh config: it lists a kept old handle next to the new one.
func sshConfig(c *config.Config) {
	if err := setup.SSHConfig(c); err != nil {
		nimble.Warn("ssh config: " + err.Error())
	}
}

// dropOldKey ends a handover early: it removes the key a renewal kept, its handle (.old)
// and the key in the Secure Enclave. The hosts that don't have the new key yet can then
// only be reached with a YubiKey, so nimble names them and asks first.
func dropOldKey(c *config.Config, handle, key string) error {
	old := handle + ".old"
	if !nimble.Exists(old + ".pub") {
		nimble.Step("no old key is kept (" + nimble.Tilde(old) + " doesn't exist)")
		return keys.EndHandover(key)
	}
	what := "the old key handle " + nimble.Tilde(old)
	if e, ok := keys.DailyKey(old); ok {
		what = "the old Secure Enclave key " + e.Label + " (" + validity(e) + ")"
	}
	nimble.Step("remove " + what)
	h, tracked := keys.FindHandover(key)
	switch waiting := h.Waiting(c); {
	case !tracked: // renewed by an older nimble: it didn't note the updated hosts
		if !nimble.Confirm("Has every managed host had nimble update since the renewal? A host that hasn't can then only be reached with a YubiKey backup.", false) {
			return errors.New("kept the old key")
		}
	case len(waiting) > 0:
		nimble.Warn("still without the new key: " + strings.Join(waiting, ", ") + " — after this only a YubiKey backup reaches them")
		if !nimble.Confirm("Remove the old key anyway?", false) {
			return errors.New("kept the old key — nimble update " + strings.Join(waiting, " ") + " first")
		}
	}
	if err := keys.DropOldEnclave(handle); err != nil {
		return err
	}
	sshConfig(c)
	nimble.Say("removed the old daily key")
	return keys.EndHandover(key)
}

// addToRepo copies a public key into keys/<kind>/<name>.pub unless it is already trusted.
func addToRepo(pubFile, kind, name string) (bool, error) {
	p := keys.Parse(nimble.ReadTrim(pubFile))
	if p.Key == "" {
		return false, errors.New("no public key in " + pubFile)
	}
	for _, k := range keys.Repo() {
		if k.ID() == p.ID() {
			nimble.Step("already in the inventory as keys/" + k.Kind + "/" + k.Name + ".pub")
			return false, nil
		}
	}
	dst := nimble.RepoFile("keys", kind, name+".pub")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, []byte(p.Type+" "+p.Key+" "+name+"\n"), 0o644); err != nil {
		return false, err
	}
	nimble.Say("added keys/" + kind + "/" + name + ".pub")
	return true, nil
}

func newYubikey() *cobra.Command {
	var renew, dropOld bool
	var handover string
	cmd := &cobra.Command{
		Use:   "yubikey [name]",
		Short: "Set up, register and renew YubiKeys as backups (guided)",
		Long: heredoc.Doc(`
			Guides you through one YubiKey after another: plug it in; if it is new, choose its
			FIDO2 PIN; nimble creates a resident key on it (PIN and touch needed for every use)
			and adds its public key to keys/yubikey/. Then label it and put it in its safe. A
			YubiKey that carries a nimble key already is registered as it is, not overwritten.

			Every host trusts every registered backup after the next nimble update all. To
			reach the hosts with a backup — on a new workstation, or when the daily key is lost
			or expired — add --yubikey to the command: nimble update all --yubikey.

			--renew <name> gives a registered YubiKey a new key, handed over like a daily key:
			the new key goes on the YubiKey next to the old one and replaces it in
			keys/yubikey/, so every host trusts it from its next nimble update; until then the
			old one still opens the hosts (--yubikey offers both). nimble update and init note
			each host that has the new key; nimble key list shows how far the handover is, and
			says when every managed host has it (--handover is the deadline, 14 days by
			default). Then plug the YubiKey in once more: --drop-old <name> deletes the old key
			from it.

			Uses ykman and an OpenSSH that talks to FIDO keys, which nimble apply sets up on
			every workstation.
		`),
		Example: heredoc.Doc(`
			nimble key yubikey safe-home              # register a YubiKey
			nimble key yubikey --renew safe-home      # give it a new key, hand over from the old one
			nimble key yubikey --drop-old safe-home   # every host has the new key: delete the old one
		`),
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return yubikeyNames(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, keygen, ok := keys.FIDOSSH()
			if _, err := exec.LookPath("ykman"); !ok || err != nil {
				return errors.New("YubiKeys need ykman and an OpenSSH that talks to FIDO keys — nimble apply sets them up, then again")
			}
			if !nimble.Interactive() {
				return errors.New("a YubiKey takes a guided dialog (PIN, touch) — run it in a terminal")
			}
			if renew || dropOld {
				if renew && dropOld {
					return errors.New("--renew or --drop-old, not both")
				}
				name, err := nimble.Arg(args, 0, "YubiKey", yubikeyNames())
				if err != nil {
					return err
				}
				if renew {
					span, err := keys.ParseHandover(handover)
					if err != nil {
						return err
					}
					return renewYubikey(keygen, name, span)
				}
				return dropOldYubikey(keygen, name)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			var added []string
			for {
				if name == "" {
					name = nimble.Ask("Name for this YubiKey (e.g. where its safe is)?", "")
					if name == "" {
						break
					}
				}
				if !nameRe.MatchString(name) { // it becomes a file name: keys/yubikey/<name>.pub
					return fmt.Errorf("name %q: letters, digits, '.', '-' and '_'", name)
				}
				if nimble.Exists(nimble.RepoFile("keys", "yubikey", name+".pub")) {
					return fmt.Errorf("keys/yubikey/%s.pub exists — another name, or nimble key remove %s first", name, name)
				}
				ok, err := registerYubikey(keygen, name)
				if err != nil {
					return err
				}
				if ok {
					added = append(added, name)
				}
				nimble.Pause("Done — unplug '" + name + "', label it and put it in its safe")
				if !nimble.Confirm("Register another YubiKey?", false) {
					break
				}
				name = ""
			}
			if len(added) == 0 {
				return nil
			}
			if err := nimble.Record("keys: add yubikey "+strings.Join(added, ", "), "keys"); err != nil {
				return err
			}
			nimble.Step("roll them out: nimble update all — then try one: nimble status <host> --yubikey")
			return nil
		},
	}
	cmd.Flags().BoolVar(&renew, "renew", false, "give a registered YubiKey a new key and hand over from the old one")
	cmd.Flags().StringVar(&handover, "handover", "14d", "with --renew: the deadline for every host to get the new key (e.g. 30d, 4w)")
	cmd.Flags().BoolVar(&dropOld, "drop-old", false, "end a handover: delete the old key from the YubiKey")
	return cmd
}

func yubikeyNames() []string {
	var names []string
	for _, k := range keys.Repo() {
		if k.Kind == "yubikey" {
			names = append(names, k.Name)
		}
	}
	return names
}

// plugIn waits until exactly one YubiKey — the one called name — is plugged in.
func plugIn(name string) {
	nimble.Pause("Plug in the YubiKey '" + name + "' — only this one")
	for n := keys.Plugged(); n != 1 && n >= 0; n = keys.Plugged() {
		if n == 0 {
			nimble.Pause("No YubiKey found — plug it in")
		} else {
			nimble.Pause(fmt.Sprintf("%d YubiKeys plugged in — unplug all but '%s'", n, name))
		}
	}
}

// identify reads the nimble keys on the plugged-in YubiKey (asks for its PIN) and checks
// that it is the registered backup name: its handles, the registered one first.
func identify(keygen, name, dir string) ([]string, error) {
	nimble.Step("read its nimble keys: enter the YubiKey's PIN")
	handles, err := keys.BackupHandles(keygen, dir)
	if err != nil {
		return nil, err
	}
	if len(handles) > 0 {
		if k, ok := keys.Registered(handles[0] + ".pub"); ok && k.Name == name {
			return handles, nil
		}
	}
	return nil, errors.New("this YubiKey isn't '" + name + "': none of its keys is keys/yubikey/" + name + ".pub — plug in the right one")
}

// renewYubikey gives the registered YubiKey name a new resident key next to its old one
// (an application of its own, so the old credential stays), puts the new public key into
// keys/yubikey/<name>.pub and starts the handover.
func renewYubikey(keygen, name string, span time.Duration) error {
	key := "yubikey/" + name
	if h, ok := keys.FindHandover(key); ok {
		return errors.New("a handover of " + name + " since " + h.Since.Format("2006-01-02") + " isn't finished — end it first: nimble key yubikey --drop-old " + name)
	}
	plugIn(name)
	tmp, err := os.MkdirTemp("", "nimble-yubikey")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	handles, err := identify(keygen, name, tmp)
	if err != nil {
		return err
	}
	old := handles[0]
	now := time.Now()
	nimble.Step("create the new key: enter the PIN, then touch the YubiKey when it blinks")
	if err := nimble.Run(keygen, "-t", "ed25519-sk", "-O", "resident", "-O", "verify-required",
		"-O", "application="+keys.RenewedApplication(now), "-C", "yubikey-"+name, "-N", "", "-f", filepath.Join(tmp, "renewed")); err != nil {
		return fmt.Errorf("%w — is the PIN right?", err)
	}
	p := keys.Parse(nimble.ReadTrim(filepath.Join(tmp, "renewed.pub")))
	if p.Key == "" {
		return errors.New("ssh-keygen wrote no public key")
	}
	if err := os.WriteFile(nimble.RepoFile("keys", "yubikey", name+".pub"), []byte(p.Type+" "+p.Key+" "+name+"\n"), 0o644); err != nil {
		return err
	}
	h := keys.Handover{Key: key, Old: keys.Fingerprint(nimble.ReadTrim(old + ".pub")), OldApp: keys.Application(old), Since: now, Until: now.Add(span)}
	if err := keys.StartHandover(h); err != nil {
		return err
	}
	if err := nimble.Record("keys: renew yubikey/"+name, "keys"); err != nil {
		return err
	}
	nimble.Say("'" + name + "' carries the new key and, until the handover ends, the old one")
	nimble.Pause("Unplug '" + name + "' — it can go back into its safe")
	nimble.Step("roll it out: nimble update all (by " + h.Until.Format("2006-01-02") + ") — nimble key list shows which hosts still trust only the old key")
	nimble.Step("then delete the old key from it: nimble key yubikey --drop-old " + name)
	return nil
}

// dropOldYubikey ends a handover: it deletes every nimble key but the registered one from
// the YubiKey name. A host that doesn't trust the new key yet can then no longer be reached
// with this YubiKey, so nimble names them and asks first.
func dropOldYubikey(keygen, name string) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	key := "yubikey/" + name
	if h, ok := keys.FindHandover(key); ok {
		if waiting := h.Waiting(c); len(waiting) > 0 {
			nimble.Warn("still without the new key: " + strings.Join(waiting, ", ") + " — after this, '" + name + "' no longer reaches them")
			if !nimble.Confirm("Delete the old key anyway?", false) {
				return errors.New("kept the old key — nimble update " + strings.Join(waiting, " ") + " first")
			}
		}
	}
	plugIn(name)
	tmp, err := os.MkdirTemp("", "nimble-yubikey")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	handles, err := identify(keygen, name, tmp)
	if err != nil {
		return err
	}
	current := keys.Application(handles[0])
	nimble.Step("list its credentials: enter the PIN once more")
	creds, err := keys.Credentials()
	if err != nil {
		return err
	}
	dropped := 0
	for _, cr := range creds {
		if !strings.HasPrefix(cr.App, keys.BackupApplication) || cr.App == current || current == "" {
			continue // not nimble's, or the key in use
		}
		nimble.Step("delete the old key " + cr.App + " (enter the PIN)")
		if err := keys.DeleteCredential(cr); err != nil {
			return err
		}
		dropped++
	}
	if dropped == 0 {
		nimble.Say("'" + name + "' carries no old nimble key")
	} else {
		nimble.Say("deleted the old key from '" + name + "'")
	}
	nimble.Pause("Unplug '" + name + "' — it can go back into its safe")
	return keys.EndHandover(key)
}

// registerYubikey sets one YubiKey up and adds its key to keys/yubikey/<name>.pub: a FIDO2
// PIN if it has none, then the nimble key it carries already, else a new resident key.
func registerYubikey(keygen, name string) (bool, error) {
	plugIn(name)
	if set, known := keys.FIDOPin(); known && !set {
		nimble.Step("this YubiKey has no FIDO2 PIN yet: choose one (at least 4 characters). It is asked for at every use, and 8 wrong tries in a row block the key")
		if err := nimble.Run("ykman", "fido", "access", "change-pin"); err != nil {
			return false, err
		}
	}
	tmp, err := os.MkdirTemp("", "nimble-yubikey")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)

	// A new key with the same application would replace the one on the YubiKey, and a public
	// key registered for it would stop working: look first.
	nimble.Step("look for a nimble key on it: enter the YubiKey's PIN")
	pub := ""
	if handles, err := keys.BackupHandles(keygen, tmp); err != nil {
		nimble.Warn(err.Error() + " — creating a new key")
	} else if len(handles) > 0 { // the registered one, else the newest
		nimble.Step("it carries a nimble key already: registering that one")
		pub = handles[0] + ".pub"
	}
	if pub == "" {
		nimble.Step("create the key: enter the PIN, then touch the YubiKey when it blinks")
		err := nimble.Run(keygen, "-t", "ed25519-sk", "-O", "resident", "-O", "verify-required",
			"-O", "application="+keys.BackupApplication, "-C", "yubikey-"+name, "-N", "", "-f", filepath.Join(tmp, "new"))
		if err != nil {
			return false, fmt.Errorf("%w — is the PIN right? (set or change it: ykman fido access change-pin)", err)
		}
		pub = filepath.Join(tmp, "new.pub")
	}
	return addToRepo(pub, "yubikey", name)
}

func newRemove() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Short: "Retire a key (lost YubiKey, old Mac)", Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			var out []string
			for _, k := range keys.Repo() {
				out = append(out, k.Name+"\t"+k.Kind)
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "key", keyNames())
			if err != nil {
				return err
			}
			c, err := config.Load()
			if err != nil {
				return err
			}
			var target *keys.Public
			for _, k := range keys.Repo() {
				if k.Name == name {
					k := k
					target = &k
				}
			}
			if target == nil {
				return errors.New("no key " + name + " (nimble key list)")
			}
			for _, d := range c.Domains {
				if left := len(keys.Trusted(d)); left == 1 && (target.Kind == "yubikey" || target.Label == d.Label) {
					return errors.New("that's the last key domain " + d.Name + " trusts — add another one first, or you lose access")
				}
			}
			if err := os.Remove(target.Path()); err != nil {
				return err
			}
			nimble.Say("removed keys/" + target.Kind + "/" + target.Name + ".pub")
			if err := keys.EndHandover(target.Kind + "/" + target.Name); err != nil { // nothing left to hand over to
				return err
			}
			if err := nimble.Record("keys: remove "+target.Kind+"/"+target.Name, "keys"); err != nil {
				return err
			}
			nimble.Step("revoke it on the hosts: nimble update all")
			return nil
		},
	}
}

func keyNames() []string {
	var names []string
	for _, k := range keys.Repo() {
		names = append(names, k.Name)
	}
	return names
}
