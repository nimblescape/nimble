package seal

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// softOpener holds private keys in memory: it stands in for the Secure Enclave.
type softOpener struct {
	keys  map[string]*ecdh.PrivateKey // by recipient name
	calls int
}

func (o *softOpener) name() string { return "memory" }

func (o *softOpener) has(r Recipient) bool {
	for _, k := range o.keys { // by public key, as the Secure Enclave: an agent request has no name
		if bytes.Equal(k.PublicKey().Bytes(), r.Public) {
			return true
		}
	}
	return false
}

func (o *softOpener) kek(r Recipient, eph []byte, prompt string) ([]byte, error) {
	o.calls++
	for _, k := range o.keys {
		if bytes.Equal(k.PublicKey().Bytes(), r.Public) {
			pub, err := ecdh.P256().NewPublicKey(eph)
			if err != nil {
				return nil, err
			}
			shared, err := k.ECDH(pub)
			if err != nil {
				return nil, err
			}
			return DeriveKEK(shared, eph, r.Public)
		}
	}
	return nil, ErrNoKey
}

func (o *softOpener) offers() []agentKey {
	var out []agentKey
	for _, k := range o.keys {
		out = append(out, agentKey{kind: KindEnclave, public: k.PublicKey().Bytes()})
	}
	return out
}

func newKey(t *testing.T, name string) (*ecdh.PrivateKey, Recipient) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := sha1.Sum(k.PublicKey().Bytes())
	return k, Recipient{Name: name, Kind: KindEnclave, Public: k.PublicKey().Bytes(), Handle: h[:]}
}

func TestSealOpen(t *testing.T) {
	ka, a := newKey(t, "enclave/mac-a")
	_, b := newKey(t, "enclave/mac-b")
	tok, err := Seal("cherry", Labels{"provider": "cherry", "domain": "nimblescape.io"}, []byte("s3cret"), []Recipient{b, a})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(tok.Marshal())
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, tok.Marshal())
	}
	if !bytes.Equal(parsed.Marshal(), tok.Marshal()) {
		t.Fatalf("marshal after parse differs:\n%s\n%s", parsed.Marshal(), tok.Marshal())
	}
	o := &softOpener{keys: map[string]*ecdh.PrivateKey{"enclave/mac-a": ka}}
	got, err := parsed.open([]opener{o})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "s3cret" {
		t.Fatalf("got %q", got)
	}
	if o.calls != 1 {
		t.Fatalf("%d key agreements, want 1", o.calls)
	}
}

func TestOpenWithoutKey(t *testing.T) {
	_, a := newKey(t, "enclave/mac-a")
	other, _ := newKey(t, "enclave/mac-a") // same name, another key
	tok, err := Seal("cherry", nil, []byte("s3cret"), []Recipient{a})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tok.open([]opener{&softOpener{keys: map[string]*ecdh.PrivateKey{"enclave/mac-a": other}}})
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
}

func TestChangedLabelBreaksToken(t *testing.T) {
	ka, a := newKey(t, "enclave/mac-a")
	tok, err := Seal("cherry", Labels{"provider": "cherry"}, []byte("s3cret"), []Recipient{a})
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(tok.Marshal()), "provider: cherry", "provider: other", 1)
	bad, err := Parse([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.open([]opener{&softOpener{keys: map[string]*ecdh.PrivateKey{"enclave/mac-a": ka}}}); err == nil {
		t.Fatal("a token with a changed label opened")
	}
}

func TestReseal(t *testing.T) {
	ka, a := newKey(t, "enclave/mac-a")
	kb, b := newKey(t, "enclave/mac-b")
	tok, err := Seal("cherry", nil, []byte("s3cret"), []Recipient{a})
	if err != nil {
		t.Fatal(err)
	}
	created := tok.Created
	if err := tok.Reseal([]byte("s3cret"), []Recipient{b}); err != nil {
		t.Fatal(err)
	}
	if tok.Created != created {
		t.Fatal("reseal changed the creation time")
	}
	if _, err := tok.open([]opener{&softOpener{keys: map[string]*ecdh.PrivateKey{"enclave/mac-a": ka}}}); err == nil {
		t.Fatal("the old key still opens the resealed token")
	}
	if got, err := tok.open([]opener{&softOpener{keys: map[string]*ecdh.PrivateKey{"enclave/mac-b": kb}}}); err != nil || string(got) != "s3cret" {
		t.Fatalf("new key: %q, %v", got, err)
	}
}

func TestParseRejects(t *testing.T) {
	_, a := newKey(t, "enclave/mac-a")
	tok, err := Seal("cherry", Labels{"provider": "cherry"}, []byte("x"), []Recipient{a})
	if err != nil {
		t.Fatal(err)
	}
	good := string(tok.Marshal())
	for name, doc := range map[string]string{
		"tab":           strings.Replace(good, "  provider", "\tprovider", 1),
		"unknown key":   good + "extra: 1\n",
		"duplicate":     good + "name: other\n",
		"version":       strings.Replace(good, "nimbleToken: 1", "nimbleToken: 2", 1),
		"quotes":        strings.Replace(good, "name: cherry", `name: "cherry"`, 1),
		"inline flow":   strings.Replace(good, "provider: cherry", "provider: {a: b}", 1),
		"deep indent":   strings.Replace(good, "  provider", "    provider", 1),
		"bad name":      strings.Replace(good, "name: cherry", "name: ../x", 1),
		"bad label":     strings.Replace(good, "provider: cherry", "Provider: cherry", 1),
		"no recipients": good[:strings.Index(good, "recipients:")] + good[strings.Index(good, "data:"):],
		"anchor":        strings.Replace(good, "provider: cherry", "provider: &a cherry", 1),
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted:\n%s", name, doc)
		}
	}
}

func TestRecipientFile(t *testing.T) {
	_, a := newKey(t, "enclave/mac-a")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "enclave"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "enclave", "mac-a.yaml"), a.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := LoadRecipients(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Name != "enclave/mac-a" || !bytes.Equal(rs[0].Public, a.Public) || !bytes.Equal(rs[0].Handle, a.Handle) {
		t.Fatalf("got %+v", rs)
	}
}

func TestFind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NIMBLE_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	_, a := newKey(t, "enclave/mac-a")
	for name, labels := range map[string]Labels{
		"cherry":  {"provider": "cherry", "domain": "nimblescape.io"},
		"cherry2": {"provider": "cherry", "domain": "example.org"},
		"hetzner": {"provider": "hetzner", "domain": "nimblescape.io"},
	} {
		tok, err := Seal(name, labels, []byte(name), []Recipient{a})
		if err != nil {
			t.Fatal(err)
		}
		if err := tok.Save(filepath.Join(Dir(), name+".yaml")); err != nil {
			t.Fatal(err)
		}
	}
	tok, err := Find(Labels{"provider": "cherry", "domain": "nimblescape.io"})
	if err != nil || tok.Name != "cherry" {
		t.Fatalf("find: %v, %v", tok, err)
	}
	if _, err := Find(Labels{"provider": "cherry"}); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := Find(Labels{"provider": "aws"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if _, err := Get("hetzner"); err != nil {
		t.Fatal(err)
	}
	if _, err := Get("nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(Dir(), "cherry.yaml")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v, %v", fi.Mode(), err)
	}
}

func TestFIDO2KeyIsStable(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	k1, err := fido2Key(secret)
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := fido2Key(secret)
	if !bytes.Equal(k1.Bytes(), k2.Bytes()) {
		t.Fatal("the same hmac-secret gave two keys")
	}
	k3, _ := fido2Key(bytes.Repeat([]byte{8}, 32))
	if bytes.Equal(k1.Bytes(), k3.Bytes()) {
		t.Fatal("two hmac-secrets gave the same key")
	}
}

func TestParseDevices(t *testing.T) {
	out := "ioreg://4294971201: vendor=0x1050, product=0x0407 (Yubico YubiKey OTP+FIDO+CCID)\n/dev/hidraw3: vendor=0x1050, product=0x0407 (Yubico YubiKey)\n\n"
	got := parseDevices(out)
	if len(got) != 2 || got[0] != "ioreg://4294971201" || got[1] != "/dev/hidraw3" {
		t.Fatalf("got %q", got)
	}
}

// upstream is a stand-in for OpenSSH's ssh-agent: it answers every request with
// SSH_AGENT_SUCCESS and the request type, and the seal extensions with SSH_AGENT_FAILURE.
func upstream(t *testing.T, dir string) string {
	sock := filepath.Join(dir, "upstream.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					msg, err := readMessage(c)
					if err != nil {
						return
					}
					answer := []byte{agentSuccess, msg[0]}
					if msg[0] == agentcExtension {
						answer = []byte{agentFailure}
					}
					writeMessage(c, answer)
				}
			}()
		}
	}()
	return sock
}

func serve(t *testing.T, sock string, opt AgentOptions) {
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go ServeAgent(c, opt)
		}
	}()
}

func TestAgent(t *testing.T) {
	dir, err := os.MkdirTemp("", "seal") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ka, a := newKey(t, "enclave/mac-a")
	_, b := newKey(t, "enclave/mac-b")
	soft := &softOpener{keys: map[string]*ecdh.PrivateKey{"enclave/mac-a": ka}}
	saved := localOpeners
	localOpeners = func() []opener { return []opener{soft} }
	defer func() { localOpeners = saved }()

	sock := filepath.Join(dir, "agent.sock")
	var log []string
	serve(t, sock, AgentOptions{Upstream: upstream(t, dir), Log: func(s string) { log = append(log, s) }})

	tok, err := Seal("cherry", nil, []byte("s3cret"), []Recipient{b, a})
	if err != nil {
		t.Fatal(err)
	}
	ao := &agentOpener{socket: sock}
	got, err := tok.open([]opener{ao})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "s3cret" {
		t.Fatalf("got %q", got)
	}
	if ao.has(b) {
		t.Fatal("the agent offers a key it doesn't have")
	}
	if len(log) == 0 {
		t.Fatal("the agent logged nothing")
	}

	// every other request goes to the upstream agent
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := writeMessage(c, []byte{11}); err != nil { // SSH_AGENTC_REQUEST_IDENTITIES
		t.Fatal(err)
	}
	answer, err := readMessage(c)
	if err != nil || !bytes.Equal(answer, []byte{agentSuccess, 11}) {
		t.Fatalf("passed on: %v, %v", answer, err)
	}

	// an agent without the extension (the upstream itself) opens nothing
	plain := &agentOpener{socket: filepath.Join(dir, "upstream.sock")}
	if _, err := tok.open([]opener{plain}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("plain agent: %v", err)
	}
}

func TestClearToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cherry")
	if err := os.WriteFile(path, []byte("abc.def-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Sealed() {
		t.Fatal("a plain file reads as sealed")
	}
	got, err := tok.Open()
	if err != nil || string(got) != "abc.def-123" {
		t.Fatalf("open: %q, %v", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tok.Open(); err == nil {
		t.Fatal("a token in clear text that others can read opened")
	}

	// sealing it in place
	ka, a := newKey(t, "enclave/mac-a")
	tok.Labels = Labels{"provider": "cherry"}
	if err := tok.Reseal(got, []Recipient{a}); err != nil {
		t.Fatal(err)
	}
	if err := tok.Save(path); err != nil {
		t.Fatal(err)
	}
	sealed, err := Load(path)
	if err != nil || !sealed.Sealed() {
		t.Fatalf("load sealed: %v", err)
	}
	if s, err := sealed.open([]opener{&softOpener{keys: map[string]*ecdh.PrivateKey{"x": ka}}}); err != nil || string(s) != "abc.def-123" {
		t.Fatalf("open sealed: %q, %v", s, err)
	}

	c, err := Clear("x", []byte("plain"))
	if err != nil || string(c.Marshal()) != "plain\n" {
		t.Fatalf("clear: %q, %v", c.Marshal(), err)
	}
}

func TestLocalAndShared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NIMBLE_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	for _, d := range []string{Dir(), LocalDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// a clear token stays local: found there, refused in the shared folder
	if err := os.WriteFile(filepath.Join(LocalDir(), "swan"), []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := Get("swan"); err != nil || tok.Sealed() {
		t.Fatalf("local clear: %v", err)
	}
	if err := os.WriteFile(filepath.Join(Dir(), "leak"), []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Get("leak"); err == nil || !strings.Contains(err.Error(), "never shares") {
		t.Fatalf("shared clear: %v", err)
	}
	all, err := List()
	if err == nil || len(all) != 1 || all[0].Name != "swan" {
		t.Fatalf("list: %d tokens, %v", len(all), err)
	}
}
