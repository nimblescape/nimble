package seal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The file is a small YAML document in block style, which this package writes and reads
// with its own strict reader: top-level scalars and two maps of scalars, nothing else.
//
//	# A token sealed by nimble: …
//	nimbleToken: 1
//	name: cherry
//	created: 2026-11-24T06:00:00Z
//	labels:
//	  domain: nimblescape.io
//	  provider: cherry
//	recipients:
//	  enclave/hs-mbp: enclave <public> <handle> <ephemeral> <wrapped>
//	data: <nonce and ciphertext>
//
// Binary values are standard base64. Comments are whole lines starting with #.

const maxFile = 1 << 20

var b64 = base64.StdEncoding

// Marshal writes the token file. A token in clear text is its secret and a line break.
func (t *Token) Marshal() []byte {
	if !t.Sealed() {
		return append(append([]byte(nil), t.clear...), '\n')
	}
	var b bytes.Buffer
	b.WriteString("# A token sealed by nimble (nimble token): only the keys under recipients open it.\n")
	fmt.Fprintf(&b, "nimbleToken: %d\nname: %s\ncreated: %s\n", Version, t.Name, t.Created.UTC().Format(time.RFC3339))
	if len(t.Labels) > 0 {
		b.WriteString("labels:\n")
		keys := make([]string, 0, len(t.Labels))
		for k := range t.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", k, t.Labels[k])
		}
	}
	b.WriteString("recipients:\n")
	for _, s := range t.stanzas {
		fmt.Fprintf(&b, "  %s: %s %s %s %s %s\n", s.Name, s.Kind,
			b64.EncodeToString(s.Public), b64.EncodeToString(s.Handle),
			b64.EncodeToString(s.Ephemeral), b64.EncodeToString(s.Wrapped))
	}
	fmt.Fprintf(&b, "data: %s\n", b64.EncodeToString(t.data))
	return b.Bytes()
}

// Save writes the token file (mode 600), through a temporary file in the same folder.
func (t *Token) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, t.Marshal(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	t.Path = path
	return nil
}

// Load reads a token file: a sealed token, or else a token in clear text.
func Load(path string) (*Token, error) {
	data, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	if !IsSealed(data) {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		secret := bytes.TrimSuffix(bytes.TrimSuffix(data, []byte("\n")), []byte("\r"))
		return &Token{Name: name, Labels: Labels{}, Path: path, clear: append([]byte{}, secret...)}, nil
	}
	t, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	t.Path = path
	return t, nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, maxFile)
	}
	return data, nil
}

// document is the parsed file: top-level scalars and maps.
type document struct {
	scalars map[string]string
	maps    map[string]map[string]string
	order   map[string][]string // the keys of each map in file order
}

// parseDocument reads the restricted YAML: "key: value" at the left margin, "key:" opening
// a map whose "key: value" lines are indented by two spaces, comment lines and empty lines.
func parseDocument(data []byte, scalars, maps []string) (*document, error) {
	if len(data) > maxFile {
		return nil, errors.New("file too large")
	}
	known := map[string]string{}
	for _, k := range scalars {
		known[k] = "scalar"
	}
	for _, k := range maps {
		known[k] = "map"
	}
	d := &document{scalars: map[string]string{}, maps: map[string]map[string]string{}, order: map[string][]string{}}
	current := "" // the map being read
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		if strings.ContainsAny(line, "\r\t") {
			return nil, fmt.Errorf("line %d: tabs and carriage returns are not allowed", n)
		}
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(trimmed)
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || key == "" || strings.ContainsAny(key, " #'\"") {
			return nil, fmt.Errorf("line %d: expected key: value", n)
		}
		if value != "" && !strings.HasPrefix(value, " ") {
			return nil, fmt.Errorf("line %d: expected a space after the colon", n)
		}
		value = strings.TrimSpace(value)
		if strings.ContainsAny(value, "#'\"{}[]&*!|>%`") {
			return nil, fmt.Errorf("line %d: quotes, flow style, anchors and inline comments are not allowed", n)
		}
		switch indent {
		case 0:
			kind, ok := known[key]
			if !ok {
				return nil, fmt.Errorf("line %d: unknown key %q", n, key)
			}
			if _, dup := d.scalars[key]; dup {
				return nil, fmt.Errorf("line %d: %s twice", n, key)
			}
			if _, dup := d.maps[key]; dup {
				return nil, fmt.Errorf("line %d: %s twice", n, key)
			}
			current = ""
			if kind == "map" {
				if value != "" {
					return nil, fmt.Errorf("line %d: %s holds key: value lines below it", n, key)
				}
				d.maps[key] = map[string]string{}
				current = key
			} else {
				if value == "" {
					return nil, fmt.Errorf("line %d: %s has no value", n, key)
				}
				d.scalars[key] = value
			}
		case 2:
			if current == "" {
				return nil, fmt.Errorf("line %d: unexpected indentation", n)
			}
			if _, dup := d.maps[current][key]; dup {
				return nil, fmt.Errorf("line %d: %s.%s twice", n, current, key)
			}
			if value == "" {
				return nil, fmt.Errorf("line %d: %s.%s has no value", n, current, key)
			}
			d.maps[current][key] = value
			d.order[current] = append(d.order[current], key)
		default:
			return nil, fmt.Errorf("line %d: indent by two spaces", n)
		}
	}
	return d, nil
}

func decode64(what, s string) ([]byte, error) {
	b, err := b64.Strict().DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%s: not base64", what)
	}
	return b, nil
}

// Parse reads a token from the bytes of its file.
func Parse(data []byte) (*Token, error) {
	d, err := parseDocument(data, []string{"nimbleToken", "name", "created", "data"}, []string{"labels", "recipients"})
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"nimbleToken", "name", "data"} {
		if d.scalars[k] == "" {
			return nil, fmt.Errorf("no %s", k)
		}
	}
	if v, err := strconv.Atoi(d.scalars["nimbleToken"]); err != nil || v != Version {
		return nil, fmt.Errorf("nimbleToken version %q: this program reads version %d", d.scalars["nimbleToken"], Version)
	}
	t := &Token{Name: d.scalars["name"], Labels: Labels{}}
	if err := CheckName(t.Name); err != nil {
		return nil, err
	}
	if c := d.scalars["created"]; c != "" {
		if t.Created, err = time.Parse(time.RFC3339, c); err != nil {
			return nil, fmt.Errorf("created %q: not an RFC 3339 time", c)
		}
	}
	for k, v := range d.maps["labels"] {
		if err := CheckLabel(k, v); err != nil {
			return nil, err
		}
		t.Labels[k] = v
	}
	for _, name := range d.order["recipients"] {
		f := strings.Fields(d.maps["recipients"][name])
		if len(f) != 5 {
			return nil, fmt.Errorf("recipient %s: expected kind, public key, handle, ephemeral key and wrapped key", name)
		}
		s := stanza{Recipient: Recipient{Name: name, Kind: f[0]}}
		for i, p := range []*[]byte{&s.Public, &s.Handle, &s.Ephemeral, &s.Wrapped} {
			if *p, err = decode64("recipient "+name, f[i+1]); err != nil {
				return nil, err
			}
		}
		if err := checkRecipient(s.Recipient); err != nil {
			return nil, err
		}
		if len(s.Ephemeral) != 65 || len(s.Wrapped) != 48 {
			return nil, fmt.Errorf("recipient %s: wrong length of the ephemeral or wrapped key", name)
		}
		t.stanzas = append(t.stanzas, s)
	}
	if len(t.stanzas) == 0 {
		return nil, errors.New("no recipients")
	}
	if t.data, err = decode64("data", d.scalars["data"]); err != nil {
		return nil, err
	}
	return t, nil
}

// Marshal writes the file of a recipient key (keys/seal/<kind>/<name>.yaml in the config
// repo): what sealing needs, and what opening needs to find the private key.
func (r Recipient) Marshal() []byte {
	return []byte(fmt.Sprintf("# A key that nimble seals tokens for (nimble token key).\nkind: %s\npublic: %s\nhandle: %s\n",
		r.Kind, b64.EncodeToString(r.Public), b64.EncodeToString(r.Handle)))
}

// ParseRecipient reads the file of a recipient key; name is its recipient name.
func ParseRecipient(name string, data []byte) (Recipient, error) {
	d, err := parseDocument(data, []string{"kind", "public", "handle"}, nil)
	if err != nil {
		return Recipient{}, err
	}
	r := Recipient{Name: name, Kind: d.scalars["kind"]}
	if r.Public, err = decode64("public", d.scalars["public"]); err != nil {
		return r, err
	}
	if r.Handle, err = decode64("handle", d.scalars["handle"]); err != nil {
		return r, err
	}
	return r, checkRecipient(r)
}

// LoadRecipients reads the recipient keys under dir: dir/<kind>/<name>.yaml, named
// <kind>/<name>. A missing dir holds none.
func LoadRecipients(dir string) ([]Recipient, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.yaml"))
	sort.Strings(files)
	var out []Recipient
	for _, f := range files {
		data, err := readLimited(f)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(filepath.Dir(f)) + "/" + strings.TrimSuffix(filepath.Base(f), ".yaml")
		r, err := ParseRecipient(name, data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// IsSealed reports whether data is a sealed token: its first line that is neither empty nor
// a comment starts with "nimbleToken:".
func IsSealed(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") {
			return strings.HasPrefix(line, "nimbleToken:")
		}
	}
	return false
}
