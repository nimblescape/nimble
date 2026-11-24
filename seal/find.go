package seal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// nimble keeps tokens in two folders:
//
//   - Dir, the tokens folder of the nimble config repo: shared. nimble push publishes it,
//     so it holds sealed tokens only.
//   - LocalDir, outside the repo: this machine only. It holds tokens in clear text, and
//     sealed tokens that are not to be shared.
//
// Other programs may keep tokens in folders of their own (ListIn, GetIn, FindIn) and still
// seal them for the keys nimble registers (Recipients).

// repo is the nimble config repo: $NIMBLE_HOME, else ~/.nimble.
func repo() string {
	if home := os.Getenv("NIMBLE_HOME"); home != "" {
		return home
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".nimble")
}

// Dir is the shared token folder: tokens/ in the nimble config repo.
func Dir() string { return filepath.Join(repo(), "tokens") }

// LocalDir is the token folder of this machine only: $XDG_CONFIG_HOME/nimble/tokens, else
// ~/.config/nimble/tokens.
func LocalDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "nimble", "tokens")
}

// KeysDir is where the nimble config repo lists the keys that tokens are sealed for:
// keys/seal/<kind>/<name>.yaml (see LoadRecipients).
func KeysDir() string { return filepath.Join(repo(), "keys", "seal") }

// Recipients are the keys that nimble seals tokens for: every key in KeysDir.
func Recipients() ([]Recipient, error) { return LoadRecipients(KeysDir()) }

// errSharedClear refuses a token in clear text in the shared folder.
func errSharedClear(t *Token) error {
	return fmt.Errorf("%s is a token in clear text in the shared folder, which nimble never shares: seal it (nimble token seal %s) or move it to %s",
		t.Path, t.Path, LocalDir())
}

// List reads the tokens of LocalDir and Dir. Tokens in clear text in Dir are refused (in
// err), as are unreadable files.
func List() ([]*Token, error) {
	local, err1 := ListIn(LocalDir())
	shared, err2 := ListIn(Dir())
	errs := []error{err1, err2}
	out := local
	for _, t := range shared {
		if !t.Sealed() {
			errs = append(errs, errSharedClear(t))
			continue
		}
		out = append(out, t)
	}
	return out, errors.Join(errs...)
}

// ListIn reads every token in dir: the files <name>.yaml, and files without an extension
// (a token in clear text, or a sealed token named after its file). Unreadable files are
// reported in err, next to the tokens that could be read.
func ListIn(dir string) ([]*Token, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && !strings.HasPrefix(n, ".") && (strings.HasSuffix(n, ".yaml") || filepath.Ext(n) == "") {
			files = append(files, filepath.Join(dir, n))
		}
	}
	sort.Strings(files)
	var out []*Token
	var errs []error
	for _, f := range files {
		t, err := Load(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, t)
	}
	return out, errors.Join(errs...)
}

// Get reads the token with this name: from LocalDir, else from Dir.
func Get(name string) (*Token, error) {
	t, err := GetIn(LocalDir(), name)
	if !errors.Is(err, ErrNotFound) {
		return t, err
	}
	if t, err = GetIn(Dir(), name); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: no token %s in %s or %s", ErrNotFound, name, LocalDir(), Dir())
		}
		return nil, err
	}
	if !t.Sealed() {
		return nil, errSharedClear(t)
	}
	return t, nil
}

// GetIn reads the token with this name from dir: <name>.yaml, else the file <name>.
func GetIn(dir, name string) (*Token, error) {
	if err := CheckName(name); err != nil {
		return nil, err
	}
	for _, f := range []string{name + ".yaml", name} {
		path := filepath.Join(dir, f)
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			return Load(path)
		}
	}
	return nil, fmt.Errorf("%w: no token %s in %s", ErrNotFound, name, dir)
}

// Find returns the one token in LocalDir and Dir that has every label in want. It returns
// an error that wraps ErrNotFound when none has, and ErrAmbiguous when more than one has.
func Find(want Labels) (*Token, error) {
	all, err := List()
	return pick(all, err, want, LocalDir()+" or "+Dir())
}

// FindIn returns the one token in dir that has every label in want (see Find).
func FindIn(dir string, want Labels) (*Token, error) {
	all, err := ListIn(dir)
	return pick(all, err, want, dir)
}

func pick(all []*Token, listErr error, want Labels, where string) (*Token, error) {
	var found []*Token
	for _, t := range all {
		if t.Matches(want) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		err := fmt.Errorf("%w: no token in %s has the labels %s", ErrNotFound, where, want)
		if listErr != nil {
			return nil, fmt.Errorf("%w; unreadable: %w", err, listErr)
		}
		return nil, err
	default:
		names := make([]string, len(found))
		for i, t := range found {
			names[i] = t.Path
		}
		return nil, fmt.Errorf("%w: %s all have the labels %s", ErrAmbiguous, strings.Join(names, ", "), want)
	}
}
