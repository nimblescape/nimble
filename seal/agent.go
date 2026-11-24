package seal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// The agent protocol extends the ssh agent protocol (draft-miller-ssh-agent) with two
// extension requests (SSH_AGENTC_EXTENSION), so that the requests travel through every
// forwarded agent socket that ssh sets up:
//
//	seal-keys@nimblescape.com  no contents
//	    → SSH_AGENT_SUCCESS, uint32 n, n × (string kind, string public key)
//	      the keys the agent opens tokens with; kind fido2 with an empty public key
//	      means that a YubiKey is plugged in
//	seal-kek@nimblescape.com   string kind, string public key, string handle,
//	                           string ephemeral key, string token name
//	    → SSH_AGENT_SUCCESS, string key-wrapping key (DeriveKEK)
//	    → SSH_AGENT_EXTENSION_FAILURE, string message
//
// An agent that doesn't know them, such as OpenSSH's ssh-agent, answers SSH_AGENT_FAILURE.
// The private keys stay with the agent: it returns only the key-wrapping key of one
// stanza, after Touch ID (or a YubiKey's PIN and touch) on its machine.

// Extension names of the agent protocol.
const (
	AgentKeysExtension = "seal-keys@nimblescape.com"
	AgentKEKExtension  = "seal-kek@nimblescape.com"
)

const (
	agentFailure          = 5
	agentSuccess          = 6
	agentcExtension       = 27
	agentExtensionFailure = 28
	agentMaxMessage       = 256 * 1024
)

func readMessage(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > agentMaxMessage {
		return nil, fmt.Errorf("agent message of %d bytes", n)
	}
	msg := make([]byte, n)
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func writeMessage(w io.Writer, msg []byte) error {
	buf := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(buf, uint32(len(msg)))
	copy(buf[4:], msg)
	_, err := w.Write(buf)
	return err
}

func putString(b *bytes.Buffer, s []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	b.Write(n[:])
	b.Write(s)
}

// reader takes strings and integers off a message.
type reader struct {
	b   []byte
	err error
}

func (r *reader) uint32() uint32 {
	if r.err != nil || len(r.b) < 4 {
		r.err = errors.New("short agent message")
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *reader) string() []byte {
	n := r.uint32()
	if r.err != nil || uint32(len(r.b)) < n {
		r.err = errors.New("short agent message")
		return nil
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s
}

// agentKey is one entry of the seal-keys answer.
type agentKey struct {
	kind   string
	public []byte
}

// agentOpener asks the agent at socket to run the key agreement.
type agentOpener struct {
	socket string
	listed bool
	keys   []agentKey
}

func (o *agentOpener) name() string { return "the agent at " + o.socket }

// offers is empty: an agent answers with the keys of its own machine, it doesn't pass requests on.
func (o *agentOpener) offers() []agentKey { return nil }

// request sends one extension request and returns the answer.
func (o *agentOpener) request(ext string, contents []byte, timeout time.Duration) ([]byte, error) {
	conn, err := net.DialTimeout("unix", o.socket, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	var b bytes.Buffer
	b.WriteByte(agentcExtension)
	putString(&b, []byte(ext))
	b.Write(contents)
	if err := writeMessage(conn, b.Bytes()); err != nil {
		return nil, err
	}
	return readMessage(conn)
}

func (o *agentOpener) has(r Recipient) bool {
	if !o.listed {
		o.listed = true
		if answer, err := o.request(AgentKeysExtension, nil, 10*time.Second); err == nil && answer[0] == agentSuccess {
			rd := &reader{b: answer[1:]}
			for n := rd.uint32(); n > 0 && n < 1024 && rd.err == nil; n-- {
				k := agentKey{kind: string(rd.string()), public: append([]byte(nil), rd.string()...)}
				if rd.err == nil {
					o.keys = append(o.keys, k)
				}
			}
		}
	}
	for _, k := range o.keys {
		if k.kind == r.Kind && (r.Kind == KindFIDO2 || bytes.Equal(k.public, r.Public)) {
			return true
		}
	}
	return false
}

func (o *agentOpener) kek(r Recipient, eph []byte, prompt string) ([]byte, error) {
	var b bytes.Buffer
	putString(&b, []byte(r.Kind))
	putString(&b, r.Public)
	putString(&b, r.Handle)
	putString(&b, eph)
	putString(&b, []byte(prompt))
	answer, err := o.request(AgentKEKExtension, b.Bytes(), 3*time.Minute) // Touch ID or PIN on the other side
	if err != nil {
		return nil, err
	}
	rd := &reader{b: answer[1:]}
	switch answer[0] {
	case agentSuccess:
		kek := rd.string()
		if rd.err != nil || len(kek) != 32 {
			return nil, errors.New("the agent sent a malformed answer")
		}
		return append([]byte(nil), kek...), nil
	case agentExtensionFailure:
		if msg := rd.string(); rd.err == nil && len(msg) > 0 {
			return nil, fmt.Errorf("the agent refused: %s", msg)
		}
		return nil, errors.New("the agent refused")
	default:
		return nil, errors.New("the agent doesn't open sealed tokens (not a nimble agent)")
	}
}

// AgentOptions configures ServeAgent.
type AgentOptions struct {
	// Upstream is the socket of the ssh agent that answers every other request (signing
	// with the ssh keys), usually the SSH_AUTH_SOCK the agent started with. Empty: none.
	Upstream string
	// FIDO2 lets the agent use a plugged-in YubiKey. Its PIN is asked on the terminal, so
	// only an agent that runs in a terminal can use it.
	FIDO2 bool
	// Log receives one line per sealed-token request.
	Log func(string)
}

// ServeAgent answers the requests of one client connection of an ssh agent socket until the
// client closes it. It answers the seal extension requests with the keys of this machine
// (the Secure Enclave, and a YubiKey with FIDO2) and passes every other request to the
// upstream agent unchanged.
func ServeAgent(client net.Conn, opt AgentOptions) error {
	defer client.Close()
	var up net.Conn
	defer func() {
		if up != nil {
			up.Close()
		}
	}()
	logf := func(format string, args ...any) {
		if opt.Log != nil {
			opt.Log(fmt.Sprintf(format, args...))
		}
	}
	for {
		msg, err := readMessage(client)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var answer []byte
		if msg[0] == agentcExtension {
			rd := &reader{b: msg[1:]}
			switch ext := string(rd.string()); {
			case rd.err != nil:
				answer = []byte{agentFailure}
			case ext == AgentKeysExtension:
				answer = agentKeysAnswer(opt)
			case ext == AgentKEKExtension:
				answer = agentKEKAnswer(rd, opt, logf)
			}
		}
		if answer == nil { // not ours: the upstream agent answers
			if up == nil && opt.Upstream != "" {
				up, _ = net.DialTimeout("unix", opt.Upstream, 5*time.Second)
			}
			if up != nil {
				if err := writeMessage(up, msg); err == nil {
					answer, err = readMessage(up)
					if err != nil {
						up.Close()
						up, answer = nil, nil
					}
				}
			}
			if answer == nil {
				answer = []byte{agentFailure}
			}
		}
		if err := writeMessage(client, answer); err != nil {
			return err
		}
	}
}

func agentKeysAnswer(opt AgentOptions) []byte {
	var keys []agentKey
	for _, o := range localOpeners() {
		for _, k := range o.offers() {
			if k.kind != KindFIDO2 || opt.FIDO2 {
				keys = append(keys, k)
			}
		}
	}
	var b bytes.Buffer
	b.WriteByte(agentSuccess)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(keys)))
	b.Write(n[:])
	for _, k := range keys {
		putString(&b, []byte(k.kind))
		putString(&b, k.public)
	}
	return b.Bytes()
}

func agentKEKAnswer(rd *reader, opt AgentOptions, logf func(string, ...any)) []byte {
	fail := func(msg string) []byte {
		var b bytes.Buffer
		b.WriteByte(agentExtensionFailure)
		putString(&b, []byte(msg))
		return b.Bytes()
	}
	kind, public, handle, eph, prompt := string(rd.string()), rd.string(), rd.string(), rd.string(), string(rd.string())
	if rd.err != nil {
		return fail("malformed request")
	}
	if len(prompt) > 200 {
		prompt = prompt[:200]
	}
	r := Recipient{Name: kind + "/request", Kind: kind, Public: append([]byte(nil), public...), Handle: append([]byte(nil), handle...)}
	if err := checkRecipient(r); err != nil {
		return fail(err.Error())
	}
	if kind == KindFIDO2 && !opt.FIDO2 {
		return fail("this agent doesn't use YubiKeys")
	}
	logf("request: %s (Touch ID or YubiKey)", prompt)
	kek, err := localKEK(r, append([]byte(nil), eph...), prompt+" (requested through the agent)")
	if err != nil {
		logf("refused: %v", err)
		return fail(err.Error())
	}
	defer clear(kek)
	logf("answered: %s", prompt)
	var b bytes.Buffer
	b.WriteByte(agentSuccess)
	putString(&b, kek)
	return b.Bytes()
}
