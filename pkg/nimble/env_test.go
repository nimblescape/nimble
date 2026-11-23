package nimble

import "testing"

// Debian and Ubuntu both say who they are and which release in /etc/os-release.
func TestParseOSRelease(t *testing.T) {
	for _, c := range []struct{ text, id, codename string }{
		{"PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"13\"\nVERSION_CODENAME=trixie\nID=debian\n", "debian", "trixie"},
		{"PRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\nUBUNTU_CODENAME=noble\n", "ubuntu", "noble"},
		{"ID=debian\nVERSION_CODENAME=\n", "debian", ""},
		{"", "", ""},
	} {
		if id, codename := ParseOSRelease(c.text); id != c.id || codename != c.codename {
			t.Errorf("%q: %q %q, want %q %q", c.text, id, codename, c.id, c.codename)
		}
	}
}
