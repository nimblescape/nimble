package token

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/hosts"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/seal"
	"github.com/spf13/cobra"
)

// sealLabel is the label of the sealing key in the Secure Enclave of a Mac.
const sealLabel = "nimble-seal"

// defaultKeyType is the sc_auth key type of a sealing key: P-256 in the Secure Enclave,
// like the daily ssh key.
const defaultKeyType = "p-256-ne"

var keyNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func newKey() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key <command>",
		Short: "The keys tokens are sealed for: the Secure Enclave of each Mac, YubiKeys",
		Long: heredoc.Doc(`
			keys/seal/ in the config repo lists the public keys that nimble seals tokens for:
			keys/seal/enclave/<machine>.yaml for the sealing key in the Secure Enclave of a
			Mac (Touch ID), keys/seal/yubikey/<name>.yaml for a YubiKey (PIN and touch). They
			are other keys than the ssh keys of nimble key: a sealing key runs a key agreement,
			which a signing key can't. After a change, nimble token reseal seals the tokens for
			the keys registered then.
		`),
	}
	var keyType string
	enclave := &cobra.Command{
		Use: "enclave", Short: "Create and register the sealing key of this Mac (Secure Enclave)", Args: cobra.NoArgs,
		Long: heredoc.Doc(`
			Create a key in the Secure Enclave of this Mac (sc_auth create-ctk-identity, label
			nimble-seal, Touch ID for every use) unless it has one, check with one Touch ID that
			it runs the key agreement, and register its public key in
			keys/seal/enclave/<machine>.yaml. Then nimble token reseal on a machine that opens
			the tokens seals them for this Mac too.
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !seal.EnclaveSupported() {
				return errors.New("a Secure Enclave key needs a Mac — on Linux register a YubiKey: nimble token key yubikey <name>")
			}
			if err := nimble.Editable(); err != nil {
				return err
			}
			return enclaveKey(keyType)
		},
	}
	enclave.Flags().StringVar(&keyType, "key-type", defaultKeyType, "the sc_auth key type")
	_ = enclave.Flags().MarkHidden("key-type")

	yubikey := &cobra.Command{
		Use: "yubikey <name>", Short: "Register a YubiKey as a sealing key (FIDO2, PIN and touch)", Args: cobra.MaximumNArgs(1),
		Long: heredoc.Doc(`
			Create a FIDO2 credential with the hmac-secret extension on the plugged-in YubiKey
			(its PIN, then two touches) and register the key nimble derives from it in
			keys/seal/yubikey/<name>.yaml. Name it like its ssh backup (nimble key yubikey).
			Needs the tools of libfido2 (fido2-token, fido2-cred, fido2-assert), which nimble
			apply installs.
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := nimble.Editable(); err != nil {
				return err
			}
			name, err := nimble.Arg(args, 0, "YubiKey name", nil)
			if err != nil {
				return err
			}
			if !keyNameRe.MatchString(name) {
				return fmt.Errorf("name %q: letters, digits, '.', '-' and '_'", name)
			}
			return yubikeyKey(name)
		},
	}
	cmd.AddCommand(newKeyList(), enclave, yubikey, newKeyCheck(), newKeyRemove())
	return cmd
}

func keyPath(r seal.Recipient) string {
	kind, name, _ := strings.Cut(r.Name, "/")
	return filepath.Join(seal.KeysDir(), kind, name+".yaml")
}

func newKeyList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "Show the registered sealing keys, and the keys of this Mac's Secure Enclave", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rs, err := seal.Recipients()
			if err != nil {
				return err
			}
			nimble.Header("Sealing keys — tokens are sealed for these (keys/seal/)")
			if len(rs) == 0 {
				nimble.Step("none yet: nimble token key enclave (a Mac), nimble token key yubikey <name>")
			}
			local, _ := seal.EnclaveKeys()
			for _, r := range rs {
				mark := ""
				for _, k := range local {
					if bytes.Equal(k.Public, r.Public) {
						mark = nimble.Green("  ← this Mac")
					}
				}
				fmt.Printf("  %-36s %s%s\n", r.Name, r.Kind, mark)
			}
			if seal.EnclaveSupported() {
				nimble.Header("This Mac's token keys (Secure Enclave, smart cards)")
				if len(local) == 0 {
					nimble.Step("none")
				}
				for _, k := range local {
					agree := nimble.Red("no key agreement")
					if k.KeyExchange {
						agree = nimble.Green("key agreement")
					}
					fmt.Printf("  %-20s %-22s %s  %s\n", k.Label, k.Token, agree, hex.EncodeToString(k.Hash))
				}
			}
			return nil
		},
	}
}

// enclaveKey makes sure this Mac has a sealing key in its Secure Enclave and registers it.
func enclaveKey(keyType string) error {
	machine := hosts.MachineName()
	name := "enclave/" + machine
	have, err := seal.Recipients()
	if err != nil {
		return err
	}
	keys, err := seal.EnclaveKeys()
	if err != nil {
		return err
	}
	for _, r := range have { // registered already, and here
		for _, k := range keys {
			if r.Kind == seal.KindEnclave && bytes.Equal(k.Public, r.Public) && k.KeyExchange {
				nimble.Step("this Mac's sealing key is registered as " + r.Name)
				return nil
			}
		}
	}
	var key *seal.EnclaveKey
	for i, k := range keys {
		if k.Label == sealLabel && k.KeyExchange && k.Public != nil {
			key = &keys[i]
		}
	}
	if key == nil {
		label := sealLabel
		for _, k := range keys {
			if k.Label == label { // one that can't agree keys: a new one with a label of its own
				label = sealLabel + "-" + keyType
			}
		}
		nimble.Step("create the sealing key " + label + " in the Secure Enclave (" + keyType + ", Touch ID for every use)")
		if err := nimble.Run("sc_auth", "create-ctk-identity", "-l", label, "-k", keyType, "-t", "bio"); err != nil {
			return err
		}
		if keys, err = seal.EnclaveKeys(); err != nil {
			return err
		}
		for i, k := range keys {
			if k.Label == label {
				key = &keys[i]
			}
		}
		if key == nil {
			return errors.New("macOS doesn't list the new key " + label + " (nimble token key list)")
		}
		if !key.KeyExchange || key.Public == nil {
			return fmt.Errorf("the new Secure Enclave key %s (%s) can't run a key agreement on this Mac — please report this with the output of nimble token key list", label, keyType)
		}
	}
	r := key.Recipient(name)
	nimble.Step("test the key: one key agreement (Touch ID)")
	if err := seal.Probe(r); err != nil {
		return fmt.Errorf("the sealing key %s failed its test: %w", key.Label, err)
	}
	path := keyPath(r)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, r.Marshal(), 0o644); err != nil {
		return err
	}
	nimble.Say("registered " + name + " → " + nimble.Tilde(path))
	if err := nimble.Record("keys: add seal/"+name, "keys"); err != nil {
		return err
	}
	if ts, _ := seal.List(); len(ts) > 0 {
		nimble.Step("seal the tokens for this Mac too: nimble token reseal (where a key opens them)")
	}
	return nil
}

// yubikeyKey registers the plugged-in YubiKey as the sealing key yubikey/<name>.
func yubikeyKey(name string) error {
	if !seal.FIDO2Tools() {
		return errors.New("the tools of libfido2 are missing (fido2-token, fido2-cred, fido2-assert) — nimble apply installs them")
	}
	path := filepath.Join(seal.KeysDir(), "yubikey", name+".yaml")
	if nimble.Exists(path) {
		return fmt.Errorf("keys/seal/yubikey/%s.yaml exists — another name, or nimble token key remove yubikey/%s first", name, name)
	}
	devs, err := seal.FIDO2Devices()
	if err != nil {
		return err
	}
	for len(devs) != 1 {
		if !nimble.Interactive() {
			return fmt.Errorf("%d FIDO2 devices are plugged in — plug in only the YubiKey %s", len(devs), name)
		}
		nimble.Pause(fmt.Sprintf("%d FIDO2 devices are plugged in — plug in only the YubiKey '%s'", len(devs), name))
		if devs, err = seal.FIDO2Devices(); err != nil {
			return err
		}
	}
	nimble.Step("create the sealing credential: enter the YubiKey's PIN and touch it, then once more for the test")
	r, err := seal.NewFIDO2("yubikey/"+name, devs[0])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, r.Marshal(), 0o644); err != nil {
		return err
	}
	nimble.Say("registered yubikey/" + name + " → " + nimble.Tilde(path))
	if err := nimble.Record("keys: add seal/yubikey/"+name, "keys"); err != nil {
		return err
	}
	if ts, _ := seal.List(); len(ts) > 0 {
		nimble.Step("seal the tokens for it too: nimble token reseal")
	}
	return nil
}

func newKeyCheck() *cobra.Command {
	return &cobra.Command{
		Use:   "check [name…]",
		Short: "Test the sealing keys of this machine: one key agreement each (Touch ID, YubiKey)",
		Long: heredoc.Doc(`
			Run one key agreement with each registered sealing key that this machine reaches
			(or the ones named) and compare it with its public key: it shows that the key opens
			the tokens sealed for it.
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			rs, err := seal.Recipients()
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, a := range args {
				want[a] = true
			}
			local, _ := seal.EnclaveKeys()
			fido := false
			if seal.FIDO2Tools() {
				devs, _ := seal.FIDO2Devices()
				fido = len(devs) > 0
			}
			r := &nimble.Report{}
			tested := 0
			for _, k := range rs {
				if len(want) > 0 && !want[k.Name] {
					continue
				}
				here := k.Kind == seal.KindFIDO2 && fido
				for _, e := range local {
					here = here || (k.Kind == seal.KindEnclave && bytes.Equal(e.Public, k.Public))
				}
				if !here {
					if len(want) > 0 {
						r.Bad(k.Name, "not reachable on this machine")
					}
					continue
				}
				tested++
				if err := seal.Probe(k); err != nil {
					r.Bad(k.Name, err.Error())
				} else {
					r.OK(k.Name, "opens its tokens here")
				}
			}
			if tested == 0 && len(want) == 0 {
				nimble.Step("no registered sealing key is reachable here — nimble token key list")
			}
			return r.Done("")
		},
	}
}

func newKeyRemove() *cobra.Command {
	return &cobra.Command{
		Use: "remove <kind/name>", Short: "Retire a sealing key (an old Mac, a lost YubiKey)", Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			rs, _ := seal.Recipients()
			var out []string
			for _, r := range rs {
				out = append(out, r.Name)
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := nimble.Editable(); err != nil {
				return err
			}
			rs, err := seal.Recipients()
			if err != nil {
				return err
			}
			for _, r := range rs {
				if r.Name != args[0] {
					continue
				}
				if len(rs) == 1 {
					nimble.Warn("this is the last sealing key: no new token can be sealed until another one is registered")
				}
				if err := os.Remove(keyPath(r)); err != nil {
					return err
				}
				nimble.Say("removed keys/seal/" + r.Name + ".yaml")
				if err := nimble.Record("keys: remove seal/"+r.Name, "keys"); err != nil {
					return err
				}
				nimble.Step("tokens stay sealed for it until nimble token reseal seals them for the remaining keys")
				return nil
			}
			return errors.New("no sealing key " + args[0] + " (nimble token key list)")
		},
	}
}
