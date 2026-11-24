// Package token is nimble token: secrets such as API tokens, sealed for the hardware keys
// (package seal), and nimble agent, which opens them for other machines.
package token

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/seal"
	"github.com/spf13/cobra"
)

func NewCmdToken() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token <command>",
		Short: "Secrets such as API tokens, sealed for your hardware keys",
		Long: heredoc.Doc(`
			A token is a secret such as the API key of a provider. nimble seals it for every
			registered key (nimble token key): the Secure Enclave key of each Mac (Touch ID)
			and each YubiKey (PIN and touch). Sealing needs no key at hand; opening needs one of
			them. A sealed token can be shared: it lives in tokens/ of the config repo, and
			nimble push publishes it. A token in clear text never leaves this machine: it lives
			in ~/.config/nimble/tokens (mode 600) and is used as it is.

			Every command takes a token name (from ~/.config/nimble/tokens, else tokens/) or the
			path of a token file, also one that another program keeps, such as nimblesec's.
			Programs written in Go read tokens with the package github.com/nimblescape/nimble/seal.
		`),
		Example: heredoc.Doc(`
			nimble token key enclave                                    # once per Mac: its sealing key
			nimble token add cherry provider=cherry domain=nimblescape.io   # asks for the secret
			nimble token run cherry=CHERRY_AUTH_TOKEN -- terraform plan  # Touch ID, then the command
			nimble token print cherry | pbcopy
		`),
	}
	cmd.AddCommand(newList(), newAdd(), newSeal(), newPrint(), newRun(), newReseal(), newRemove(), newKey())
	return cmd
}

// resolve finds a token by name or path. A path is anything with a slash, or a file that
// exists.
func resolve(ref string) (*seal.Token, error) {
	if isPath(ref) {
		return seal.Load(expand(ref))
	}
	return seal.Get(ref)
}

func isPath(ref string) bool {
	if strings.ContainsRune(ref, '/') || strings.HasPrefix(ref, "~") {
		return true
	}
	_, err := os.Stat(ref)
	return err == nil
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(nimble.Home(), p[1:])
	}
	return p
}

// inRepo reports whether path is in the shared token folder of the config repo.
func inRepo(path string) bool {
	rel, err := filepath.Rel(seal.Dir(), path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// record commits a change of the shared token folder.
func record(msg, path string) error {
	if !inRepo(path) {
		return nil
	}
	return nimble.Record(msg, "tokens")
}

func tokenNames() []string {
	all, _ := seal.List()
	var names []string
	for _, t := range all {
		names = append(names, t.Name)
	}
	return names
}

func completeTokens(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return tokenNames(), cobra.ShellCompDirectiveDefault
}

// readSecret takes the secret from stdin when it is not a terminal (a pipe, a file), else
// asks for it without showing it. A final line break is dropped.
func readSecret(what string) ([]byte, error) {
	if !nimble.Interactive() {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024+1))
		if err != nil {
			return nil, err
		}
		if len(b) > 64*1024 {
			return nil, errors.New("the secret on stdin is larger than 64 KiB")
		}
		b = []byte(strings.TrimRight(string(b), "\r\n"))
		if len(b) == 0 {
			return nil, errors.New("no secret on stdin")
		}
		return b, nil
	}
	s, err := nimble.Secret(what + " (not shown):")
	return []byte(s), err
}

// recipients are the keys to seal for; on a Mac without any, nimble offers to create its
// sealing key first.
func recipients() ([]seal.Recipient, error) {
	rs, err := seal.Recipients()
	if err != nil || len(rs) > 0 {
		return rs, err
	}
	if seal.EnclaveSupported() && nimble.Interactive() && nimble.Confirm("No key to seal for yet. Create the sealing key of this Mac in its Secure Enclave now?", true) {
		if err := enclaveKey(defaultKeyType); err != nil {
			return nil, err
		}
		return seal.Recipients()
	}
	return nil, errors.New("no key to seal for: nimble token key enclave (on a Mac) or nimble token key yubikey <name>")
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use: "list [folder…]", Short: "Show the tokens, what they are sealed for, and what opens them here",
		Long: heredoc.Doc(`
			Show the tokens of ~/.config/nimble/tokens and tokens/, or of the folders given: their
			labels, the keys they are sealed for, and which of those this machine reaches now
			(a Secure Enclave key of this Mac, a YubiKey that is plugged in). Asks for nothing.
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			var all []*seal.Token
			var err error
			if len(args) == 0 {
				all, err = seal.List()
			} else {
				var errs []error
				for _, d := range args {
					ts, err := seal.ListIn(expand(d))
					errs = append(errs, err)
					all = append(all, ts...)
				}
				err = errors.Join(errs...)
			}
			if err != nil {
				nimble.Warn(err.Error())
			}
			if len(all) == 0 {
				nimble.Step("no tokens — add one: nimble token add <name> [key=value…]")
				return nil
			}
			for _, t := range all {
				nimble.Header(t.Name + "  " + nimble.Tilde(t.Path))
				if !t.Sealed() {
					nimble.Info("clear text", "this machine only, used as it is")
					continue
				}
				if len(t.Labels) > 0 {
					nimble.Info("labels", t.Labels.String())
				}
				here := map[string]bool{}
				for _, n := range t.Openable() {
					here[n] = true
				}
				var rs []string
				for _, r := range t.Recipients() {
					if here[r.Name] {
						rs = append(rs, nimble.Green(r.Name+" (here)"))
					} else {
						rs = append(rs, r.Name)
					}
				}
				nimble.Info("sealed for", strings.Join(rs, ", "))
			}
			return nil
		},
	}
}

func newAdd() *cobra.Command {
	var clear, local, replace bool
	cmd := &cobra.Command{
		Use:   "add <name> [key=value…]",
		Short: "Add a token: sealed for every registered key (default), or in clear text on this machine",
		Long: heredoc.Doc(`
			Ask for the secret (or read it from stdin) and seal it for every key in
			keys/seal/ of the config repo. The sealed token goes to tokens/<name>.yaml and is
			committed: nimble push shares it. --local keeps a sealed token on this machine
			only (~/.config/nimble/tokens). --clear keeps it in clear text, on this machine
			only (~/.config/nimble/tokens/<name>, mode 600): for a machine without a key.

			Labels (key=value) let programs find the token, for example provider=cherry
			domain=nimblescape.io. Keys are lowerCamelCase.
		`),
		Example: heredoc.Doc(`
			nimble token add cherry provider=cherry domain=nimblescape.io
			op read op://farm/cherry/token | nimble token add cherry provider=cherry
			nimble token add --clear github-ci
		`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := seal.CheckName(name); err != nil {
				return err
			}
			labels, err := seal.ParseLabels(args[1:])
			if err != nil {
				return err
			}
			if clear && len(labels) > 0 {
				return errors.New("a token in clear text has no labels: seal it to label it")
			}
			dir := seal.LocalDir()
			if !clear && !local {
				if err := nimble.Editable(); err != nil {
					return err
				}
				dir = seal.Dir()
			}
			path := filepath.Join(dir, name+".yaml")
			if clear {
				path = filepath.Join(dir, name)
			}
			if t, err := seal.Get(name); err == nil && !replace {
				return fmt.Errorf("token %s exists (%s) — --replace, or another name", name, nimble.Tilde(t.Path))
			}
			var tok *seal.Token
			if clear {
				secret, err := readSecret("Secret of " + name)
				if err != nil {
					return err
				}
				if tok, err = seal.Clear(name, secret); err != nil {
					return err
				}
			} else {
				rs, err := recipients()
				if err != nil {
					return err
				}
				secret, err := readSecret("Secret of " + name)
				if err != nil {
					return err
				}
				if tok, err = seal.Seal(name, labels, secret, rs); err != nil {
					return err
				}
				clearBytes(secret)
			}
			if err := tok.Save(path); err != nil {
				return err
			}
			nimble.Say(describe(tok) + " → " + nimble.Tilde(path))
			return record("tokens: add "+name, path)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "keep it in clear text, on this machine only")
	cmd.Flags().BoolVar(&local, "local", false, "seal it, but keep it on this machine only")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace a token with this name")
	return cmd
}

func describe(t *seal.Token) string {
	if !t.Sealed() {
		return "token " + t.Name + " in clear text"
	}
	var names []string
	for _, r := range t.Recipients() {
		names = append(names, r.Name)
	}
	return "token " + t.Name + " sealed for " + strings.Join(names, ", ")
}

func clearBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func newSeal() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "seal <name|path> [key=value…]",
		Short: "Seal a token that is in clear text, in its place",
		Long: heredoc.Doc(`
			Seal a token in clear text for every registered key and write the sealed token in
			its place: the file of another program (nimblesec's, for example) stays where it
			is, and a clear token of ~/.config/nimble/tokens stays there too (move it to tokens/
			to share it). Labels (key=value) are added to the sealed token; --name names it
			when its file name is no token name.
		`),
		Example: heredoc.Doc(`
			nimble token seal ~/.nimblesec/nimblescape.io/cherry provider=cherry domain=nimblescape.io
		`),
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeTokens,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := resolve(args[0])
			if err != nil {
				return err
			}
			if t.Sealed() {
				return errors.New(t.Name + " is sealed already — nimble token reseal " + args[0] + " seals it for the current keys")
			}
			labels, err := seal.ParseLabels(args[1:])
			if err != nil {
				return err
			}
			if name != "" {
				t.Name = name
			}
			if err := seal.CheckName(t.Name); err != nil {
				return fmt.Errorf("%w — name it with --name", err)
			}
			secret, err := t.Open() // a clear token: no key, only the mode check
			if err != nil {
				return err
			}
			defer clearBytes(secret)
			rs, err := recipients()
			if err != nil {
				return err
			}
			t.Labels = labels
			if err := t.Reseal(secret, rs); err != nil {
				return err
			}
			if err := t.Save(t.Path); err != nil {
				return err
			}
			nimble.Say(describe(t) + " → " + nimble.Tilde(t.Path))
			return record("tokens: seal "+t.Name, t.Path)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the token's name (default: its file name)")
	return cmd
}

func newPrint() *cobra.Command {
	return &cobra.Command{
		Use:   "print <name|path>",
		Short: "Open a token and write its secret to stdout, for a pipe",
		Long: heredoc.Doc(`
			Open the token (Touch ID or the YubiKey for a sealed one) and write the secret to
			stdout, with a line break only when stdout is a terminal.
		`),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeTokens,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := resolve(args[0])
			if err != nil {
				return err
			}
			secret, err := t.Open()
			if err != nil {
				return err
			}
			defer clearBytes(secret)
			if nimble.IsTerminal(os.Stdout) {
				secret = append(secret, '\n')
			}
			_, err = os.Stdout.Write(secret)
			return err
		},
	}
}

func newRun() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name|path>=<VARIABLE>… -- <command> [argument…]",
		Short: "Run a command with tokens in environment variables",
		Long: heredoc.Doc(`
			Open each token (Touch ID or the YubiKey for a sealed one), put its secret into the
			environment variable named after it, and run the command in place of nimble: it
			gets the terminal, the signals and the exit status. The secrets are in no file and
			in no shell history.
		`),
		Example: heredoc.Doc(`
			nimble token run cherry=NIMBLESEC_PROVIDER_KEY -- nimblesec machine console grizzly
			nimble token run cherry=CHERRY_AUTH_TOKEN gh=GH_TOKEN -- make deploy
		`),
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 1 || dash >= len(args) {
				return errors.New("usage: nimble token run <name>=<VARIABLE>… -- <command> [argument…]")
			}
			env := os.Environ()
			for _, pair := range args[:dash] {
				ref, v, ok := strings.Cut(pair, "=")
				if !ok || !envName(v) {
					return fmt.Errorf("%q: <name or path>=<VARIABLE>", pair)
				}
				t, err := resolve(ref)
				if err != nil {
					return err
				}
				secret, err := t.Open()
				if err != nil {
					return err
				}
				env = append(env, v+"="+string(secret))
				clearBytes(secret)
			}
			bin, err := exec.LookPath(args[dash])
			if err != nil {
				return err
			}
			return syscall.Exec(bin, args[dash:], env)
		},
	}
}

func envName(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func newReseal() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "reseal [name|path|folder…]",
		Short: "Seal tokens again for the keys registered now (a new Mac, a removed key)",
		Long: heredoc.Doc(`
			Open each sealed token and seal it again for the keys in keys/seal/ now: after
			nimble token key enclave on a new Mac, or after a key is removed. Without
			arguments: every token whose keys differ from the registered ones (--all: every
			token). Each token asks for Touch ID or a YubiKey once.
		`),
		ValidArgsFunction: completeTokens,
		RunE: func(cmd *cobra.Command, args []string) error {
			rs, err := seal.Recipients()
			if err != nil {
				return err
			}
			if len(rs) == 0 {
				return errors.New("no registered key to seal for")
			}
			var ts []*seal.Token
			if len(args) == 0 {
				var lerr error
				ts, lerr = seal.List()
				if lerr != nil {
					nimble.Warn(lerr.Error())
				}
			}
			for _, a := range args {
				if fi, err := os.Stat(expand(a)); err == nil && fi.IsDir() {
					more, err := seal.ListIn(expand(a))
					if err != nil {
						nimble.Warn(err.Error())
					}
					ts = append(ts, more...)
					continue
				}
				t, err := resolve(a)
				if err != nil {
					return err
				}
				ts = append(ts, t)
			}
			want := names(rs)
			done := 0
			for _, t := range ts {
				if !t.Sealed() {
					continue // a clear token is sealed with nimble token seal
				}
				if len(args) == 0 && !all && names(t.Recipients()) == want {
					continue
				}
				nimble.Step("reseal " + t.Name + " (" + nimble.Tilde(t.Path) + ")")
				secret, err := t.Open()
				if err != nil {
					return err
				}
				err = t.Reseal(secret, rs)
				clearBytes(secret)
				if err != nil {
					return err
				}
				if err := t.Save(t.Path); err != nil {
					return err
				}
				nimble.Say(describe(t))
				if err := record("tokens: reseal "+t.Name, t.Path); err != nil {
					return err
				}
				done++
			}
			if done == 0 {
				nimble.Step("every token is sealed for the registered keys")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every token, also those sealed for the registered keys already")
	return cmd
}

func names(rs []seal.Recipient) string {
	n := make([]string, len(rs))
	for i, r := range rs {
		n[i] = r.Name + ":" + string(r.Public)
	}
	sort.Strings(n)
	return strings.Join(n, ",")
}

func newRemove() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name|path>", Short: "Delete a token", Args: cobra.ExactArgs(1),
		ValidArgsFunction: completeTokens,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := resolve(args[0])
			if err != nil {
				return err
			}
			if inRepo(t.Path) {
				if err := nimble.Editable(); err != nil {
					return err
				}
			}
			if nimble.Interactive() && !nimble.Confirm("Delete the token "+t.Name+" ("+nimble.Tilde(t.Path)+")?", false) {
				return nimble.ErrCancelled
			}
			if err := os.Remove(t.Path); err != nil {
				return err
			}
			nimble.Say("deleted " + nimble.Tilde(t.Path))
			return record("tokens: remove "+t.Name, t.Path)
		},
	}
}
