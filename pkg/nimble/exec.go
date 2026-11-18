package nimble

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Say announces a change nimble makes ("==> linked ~/.config/git/config").
func Say(msg string) { stopSpinner(); fmt.Printf("%s %s\n", Bold("==>"), msg) }

// Warn prints a warning on stderr.
func Warn(msg string) { stopSpinner(); fmt.Fprintf(os.Stderr, "%s %s\n", Yellow("warning:"), msg) }

// Cmd describes a command to run.
type Cmd struct {
	Name  string
	Args  []string
	Env   []string  // added to the environment
	Dir   string    // working directory
	Quiet bool      // discard stdout (stderr stays visible)
	Stdin io.Reader // default: the terminal
}

// Do runs the command. Under a Wait its output first stops the spinner.
func (c Cmd) Do() error {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, Stdout(), Stderr()
	if c.Stdin != nil {
		cmd.Stdin = c.Stdin
	}
	if c.Quiet {
		cmd.Stdout = io.Discard
	}
	if err := Exec(cmd); err != nil {
		return fmt.Errorf("%s %s: %w", c.Name, strings.Join(c.Args, " "), err)
	}
	return nil
}

// AsRoot runs a command as root: directly when nimble is root, else through sudo.
func AsRoot(c Cmd) error {
	if os.Geteuid() != 0 {
		stopSpinner() // sudo may ask for a password (on the terminal, not through stderr)
		args := []string{}
		if len(c.Env) > 0 {
			args = append(args, "env")
			args = append(args, c.Env...)
			c.Env = nil
		}
		c.Args = append(append(args, c.Name), c.Args...)
		c.Name = "sudo"
	}
	return c.Do()
}

// Download fetches a URL into a file.
func Download(url, path string) error {
	c := &http.Client{Timeout: 10 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// VerifySHA256 checks a downloaded file against a checksums.txt (lines "<sha256>  <name>",
// as GoReleaser and sha256sum write them).
func VerifySHA256(file, sumsFile, name string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	for _, l := range Lines(ReadTrim(sumsFile)) {
		if fields := strings.Fields(l); len(fields) == 2 && fields[0] == got && strings.TrimPrefix(fields[1], "*") == name {
			return nil
		}
	}
	return fmt.Errorf("checksum mismatch for %s — not using it", name)
}

// URLExists reports whether a URL answers 200 OK to a HEAD request.
func URLExists(url string) bool {
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Head(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// DebInstalled reports whether a Debian package is installed.
func DebInstalled(pkg string) bool {
	return strings.Contains(Output("dpkg-query", "-W", "-f=${Status}", pkg), "install ok installed")
}

// AptInstall installs the missing ones of the given Debian packages; it returns those it installed.
func AptInstall(pkgs ...string) ([]string, error) {
	var missing []string
	for _, p := range pkgs {
		if !DebInstalled(p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	return missing, Wait("apt install "+strings.Join(missing, " "), func() error {
		if err := AsRoot(Cmd{Name: "apt-get", Args: []string{"update", "-qq"}, Quiet: true}); err != nil {
			return err
		}
		args := append([]string{"install", "-y", "-qq", "--no-install-recommends"}, missing...)
		return AsRoot(Cmd{Name: "apt-get", Args: args, Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Quiet: true})
	})
}

// GoArch maps `uname -m` to Go's architecture names.
func GoArch(machine string) string {
	switch machine {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return machine
}
