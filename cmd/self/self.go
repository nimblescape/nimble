package self

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/releases"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

// Repo is where nimble's releases are published.
const Repo = "nimblescape/nimble"

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func latest() (*release, error) {
	c := &http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no published release yet")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub: %s", resp.Status)
	}
	var r release
	return &r, json.NewDecoder(resp.Body).Decode(&r)
}

// channel is how this nimble was installed: brew, apt, source (built from NIMBLE_SRC, for
// development) or installer (a release binary in ~/.local/bin).
func channel() string {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	switch {
	case strings.Contains(exe, "/Cellar/") || strings.HasPrefix(exe, "/opt/homebrew/"):
		return "brew"
	case strings.HasPrefix(exe, "/usr/bin/") && nimble.Output("dpkg", "-S", exe) != "":
		return "apt"
	case nimble.SourceDir() != "":
		return "source"
	}
	return "installer"
}

func NewCmdVersion(version string) *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Show the installed version and the newest release", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("nimble %s (%s/%s, installed via %s)\n", version, runtime.GOOS, runtime.GOARCH, channel())
			if r, err := latest(); err != nil {
				fmt.Println("newest release: unknown —", err)
			} else {
				fmt.Println("newest release:", r.Tag)
			}
		},
	}
}

func NewCmdUpgrade() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Install the newest release of nimble (nimble update does this too)",
		Long: heredoc.Doc(`
			Upgrade nimble through the channel that installed it: brew upgrade (Homebrew), apt
			(Debian package), a rebuild from your nimble checkout (NIMBLE_SRC, development), or by
			downloading the newest release binary and checking its checksum (installer copies).
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return upgrade()
		},
	}
}

// upgrade brings nimble up to date through the channel that installed it.
func upgrade() error {
	switch channel() {
	case "brew":
		return nimble.Run("brew", "upgrade", "nimble")
	case "apt":
		return nimble.AsRoot(nimble.Cmd{Name: "apt-get", Args: []string{"install", "--only-upgrade", "-y", "nimble"}})
	case "source":
		return setup.SelfBuild()
	}
	return installRelease()
}

// AutoUpgrade is the nimble part of nimble update: a released nimble moves to a newer
// release through its channel. A development build stays as it is (nimble apply rebuilds
// one from NIMBLE_SRC), and a failure is only a warning.
func AutoUpgrade() {
	if channel() == "source" {
		return
	}
	r, err := latest()
	if err != nil {
		return // no release yet, or GitHub can't be reached
	}
	newest := strings.TrimPrefix(r.Tag, "v")
	if releases.NewestCompatible(nimble.Version, []string{newest}, "major") != newest {
		return // up to date, or not a release build
	}
	nimble.Step("upgrade nimble " + nimble.Version + " → " + newest)
	if err := upgrade(); err != nil {
		nimble.Warn("nimble upgrade: " + err.Error())
	}
}

func installRelease() error {
	r, err := latest()
	if err != nil {
		return err
	}
	want := fmt.Sprintf("nimble_%s_%s_%s.tar.gz", strings.TrimPrefix(r.Tag, "v"), runtime.GOOS, runtime.GOARCH)
	var asset, sums string
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			asset = a.URL
		case "checksums.txt":
			sums = a.URL
		}
	}
	if asset == "" || sums == "" {
		return fmt.Errorf("release %s has no %s or checksums.txt", r.Tag, want)
	}
	tmp, err := os.MkdirTemp("", "nimble-upgrade")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tgz, sumFile := filepath.Join(tmp, want), filepath.Join(tmp, "checksums.txt")
	if err := nimble.Wait("download nimble "+r.Tag+" (checksum checked)", func() error {
		if err := nimble.Download(asset, tgz); err != nil {
			return err
		}
		if err := nimble.Download(sums, sumFile); err != nil {
			return err
		}
		if err := nimble.VerifySHA256(tgz, sumFile, want); err != nil {
			return err
		}
		return nimble.Cmd{Name: "tar", Args: []string{"-xzf", tgz, "-C", tmp, "nimble"}}.Do()
	}); err != nil {
		return err
	}
	exe, _ := os.Executable()
	data, err := os.ReadFile(filepath.Join(tmp, "nimble"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(exe+".new", data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(exe+".new", exe); err != nil {
		return err
	}
	nimble.Say("nimble " + r.Tag + " → " + nimble.Tilde(exe))
	return nil
}
