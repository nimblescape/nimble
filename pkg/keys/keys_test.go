package keys

import "testing"

const sha1List = `Key Type Public Key Hash                          Prot Label        Common Name  Email Address Valid To        Valid 
p-256-ne 68F67B886C6A035219BEB2FA604BA97DF875BAC1 none ssh-20271101 ssh-20271101               18.11.28, 00:20 YES   
p-256-ne 5DDB2CBE25D60544FA18F70723315180E1531092 bio  ssh          ssh                        14.11.27, 01:16 NO    
`

const sshList = `Key Type Public Key Hash                                    Prot Label        Common Name  Email Address Valid To        Valid 
p-256-ne SHA256:KnDWMeKPMdSBaJKCFPf27/qmIB0wFpRSFOGmfVx+n9k none ssh-20271101 ssh-20271101               18.11.28, 00:20 YES   
p-256-ne SHA256:uw3YKFwpaO0mXMnYpPmMxeLWQDTq/GKxYznouG/dIfc bio  ssh          ssh                        14.11.27, 01:16 NO    
`

func TestParseEnclaves(t *testing.T) {
	es := parseEnclaves(sha1List, sshList)
	if len(es) != 2 {
		t.Fatalf("%d identities", len(es))
	}
	e := es[1]
	if e.Label != "ssh" || e.Hash != "5DDB2CBE25D60544FA18F70723315180E1531092" || e.Protection != "bio" || e.Valid ||
		e.Fingerprint != "SHA256:uw3YKFwpaO0mXMnYpPmMxeLWQDTq/GKxYznouG/dIfc" || e.ValidTo.Format("2006-01-02") != "2027-11-14" {
		t.Errorf("%+v", e)
	}
	if !es[0].Valid || es[0].Label != "ssh-20271101" || es[0].Fingerprint == "" {
		t.Errorf("%+v", es[0])
	}
}

func TestFingerprint(t *testing.T) {
	// the SHA256 of the key blob, base64 without padding — here of the blob "hello" (aGVsbG8=)
	if got := Fingerprint("ssh-test aGVsbG8= comment"); got != "SHA256:LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ" {
		t.Error(got)
	}
	if Fingerprint("# no key") != "" {
		t.Error("a line without a key has no fingerprint")
	}
}
