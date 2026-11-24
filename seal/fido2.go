package seal

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// A FIDO2 recipient is a P-256 key that nimble derives from the hmac-secret of a FIDO2
// credential: the YubiKey computes an HMAC of a fixed salt with a secret that never leaves
// it, after its PIN and a touch. The credential is not resident: its ID is the recipient's
// handle, and the YubiKey can only use it together with that ID. The tools of libfido2
// (fido2-token, fido2-cred, fido2-assert) talk to the YubiKey; they ask for the PIN on the
// terminal.

// FIDO2RelyingParty is the relying party ID of the credentials.
const FIDO2RelyingParty = "nimble:seal"

var fido2Salt = sha256.Sum256([]byte("nimble seal 1 fido2 hmac-secret salt"))

// FIDO2Tools reports whether the tools of libfido2 are installed.
func FIDO2Tools() bool {
	for _, t := range []string{"fido2-token", "fido2-cred", "fido2-assert"} {
		if _, err := exec.LookPath(t); err != nil {
			return false
		}
	}
	return true
}

// FIDO2Devices lists the FIDO2 devices that are plugged in (fido2-token -L): their paths.
func FIDO2Devices() ([]string, error) {
	out, err := exec.Command("fido2-token", "-L").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the FIDO2 devices (fido2-token -L): %w", err)
	}
	return parseDevices(string(out)), nil
}

// parseDevices reads the lines of fido2-token -L: "<path>: vendor=…, product=… (…)".
func parseDevices(out string) []string {
	var devs []string
	for _, line := range strings.Split(out, "\n") {
		if path, _, ok := strings.Cut(strings.TrimSpace(line), ": "); ok && path != "" {
			devs = append(devs, path)
		}
	}
	return devs
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported systems
	}
	return b
}

// fido2Run runs a libfido2 tool with input on stdin and returns the non-empty lines of its
// output. The PIN prompt goes to the terminal, messages to stderr.
func fido2Run(input string, name string, args ...string) ([]string, error) {
	c := exec.Command(name, args...)
	c.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, os.Stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	var lines []string
	for _, l := range strings.Split(out.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// NewFIDO2 creates a credential with the hmac-secret extension on the FIDO2 device (PIN,
// then touch for the credential, again for the hmac-secret) and returns the recipient for
// it, named name.
func NewFIDO2(name, device string) (Recipient, error) {
	input := b64.EncodeToString(random(32)) + "\n" + FIDO2RelyingParty + "\nnimble-seal\n" + b64.EncodeToString(random(32)) + "\n"
	lines, err := fido2Run(input, "fido2-cred", "-M", "-h", "-v", device, "es256")
	if err != nil {
		return Recipient{}, err
	}
	if len(lines) < 5 {
		return Recipient{}, errors.New("fido2-cred returned no credential")
	}
	cred, err := decode64("credential ID", lines[4])
	if err != nil {
		return Recipient{}, err
	}
	key, err := fido2Derive(device, cred)
	if err != nil {
		return Recipient{}, err
	}
	r := Recipient{Name: name, Kind: KindFIDO2, Public: key.PublicKey().Bytes(), Handle: cred}
	return r, checkRecipient(r)
}

// fido2Derive gets the hmac-secret of the credential from the device (PIN, touch) and
// derives the P-256 key from it.
func fido2Derive(device string, cred []byte) (*ecdh.PrivateKey, error) {
	input := b64.EncodeToString(random(32)) + "\n" + FIDO2RelyingParty + "\n" + b64.EncodeToString(cred) + "\n" + b64.EncodeToString(fido2Salt[:]) + "\n"
	lines, err := fido2Run(input, "fido2-assert", "-G", "-h", "-p", "-v", device)
	if err != nil {
		return nil, err
	}
	if len(lines) < 5 {
		return nil, errors.New("fido2-assert returned no hmac-secret")
	}
	secret, err := decode64("hmac-secret", lines[len(lines)-1]) // the last line without -b
	if err != nil {
		return nil, err
	}
	defer clear(secret)
	if len(secret) != 32 {
		return nil, fmt.Errorf("fido2-assert returned an hmac-secret of %d bytes, not 32", len(secret))
	}
	return fido2Key(secret)
}

// fido2Opener derives the key of a FIDO2 recipient on a plugged-in YubiKey.
type fido2Opener struct {
	listed  bool
	devices []string
}

func (o *fido2Opener) name() string { return "a YubiKey" }

func (o *fido2Opener) has(r Recipient) bool {
	if r.Kind != KindFIDO2 {
		return false
	}
	if !o.listed {
		o.listed = true
		if FIDO2Tools() {
			o.devices, _ = FIDO2Devices()
		}
	}
	return len(o.devices) > 0
}

func (o *fido2Opener) kek(r Recipient, eph []byte, prompt string) ([]byte, error) {
	pub, err := ecdh.P256().NewPublicKey(eph)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, dev := range o.devices {
		fmt.Fprintf(os.Stderr, "nimble: %s with the YubiKey %s: enter its PIN, then touch it\n", prompt, r.Name)
		key, err := fido2Derive(dev, r.Handle)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !bytes.Equal(key.PublicKey().Bytes(), r.Public) {
			errs = append(errs, fmt.Errorf("the YubiKey at %s is not %s", dev, r.Name))
			continue
		}
		shared, err := key.ECDH(pub)
		if err != nil {
			return nil, err
		}
		defer clear(shared)
		return DeriveKEK(shared, eph, r.Public)
	}
	return nil, errors.Join(errs...)
}

func (o *fido2Opener) offers() []agentKey {
	if o.has(Recipient{Kind: KindFIDO2}) {
		return []agentKey{{kind: KindFIDO2}}
	}
	return nil
}
