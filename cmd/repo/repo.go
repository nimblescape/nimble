package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/spf13/cobra"
)

func NewCmdPath() *cobra.Command {
	return &cobra.Command{
		Use:     "path",
		Short:   "Print the config repo path",
		Example: `  cd "$(nimble path)"`,
		Args:    cobra.NoArgs,
		Run:     func(cmd *cobra.Command, args []string) { fmt.Println(nimble.Repo()) },
	}
}

// Editor opens files (or the config repo) for editing: VS Code on a desktop where it is
// installed, and in VS Code's own terminal (also over Remote-SSH); $VISUAL otherwise, vi
// when that is unset. Files open with --wait, so the caller can check them afterwards.
func Editor(paths ...string) error {
	cmd := editorCommand(os.Getenv, runtime.GOOS, vscode(), paths)
	return nimble.Run(cmd[0], cmd[1:]...)
}

func editorCommand(getenv func(string) string, goos, code string, paths []string) []string {
	if code != "" && (getenv("TERM_PROGRAM") == "vscode" || desktop(getenv, goos)) {
		cmd := []string{code}
		for _, p := range paths {
			if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
				cmd = append(cmd, "--wait") // a file: back once its tab is closed; a folder just opens
				break
			}
		}
		return append(cmd, paths...)
	}
	editor := strings.Fields(getenv("VISUAL"))
	if len(editor) == 0 {
		editor = []string{"vi"}
	}
	return append(editor, paths...)
}

// desktop reports whether nimble runs on this machine's own screen, not in a session over
// ssh: macOS, or Linux with a display.
func desktop(getenv func(string) string, goos string) bool {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return false
	}
	return goos == "darwin" || getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

// vscode finds VS Code's command line: code on PATH, or, on macOS, inside the app when its
// shell command isn't installed. "" when VS Code isn't installed.
func vscode() string {
	if p, err := exec.LookPath("code"); err == nil {
		return p
	}
	if runtime.GOOS == "darwin" {
		for _, dir := range []string{"/Applications", filepath.Join(nimble.Home(), "Applications")} {
			if p := filepath.Join(dir, "Visual Studio Code.app", "Contents", "Resources", "app", "bin", "code"); nimble.Exists(p) {
				return p
			}
		}
	}
	return ""
}

func NewCmdEdit() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [shell|local|personal]",
		Short: "Open the config repo (or one of its shell files) in your editor (VS Code on a desktop)",
		Long: `Without an argument the whole config repo; "shell" the shared shell config
(shell/common.sh), "personal" your own shell additions, "local" this machine's ~/.nimble/local.sh.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"shell", "personal", "local"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] != "local" { // local.sh is this machine's own, also on a host
				if err := nimble.Editable(); err != nil {
					return err
				}
			}
			target := nimble.Repo()
			if len(args) == 1 {
				switch args[0] {
				case "shell":
					target = nimble.RepoFile("shell", "common.sh")
				case "personal":
					target = filepath.Join(config.PersonalDir(), "shell", "common.sh")
				case "local":
					target = nimble.RepoFile("local.sh")
					if !nimble.Exists(target) {
						_ = os.WriteFile(target, []byte("# Shell additions for this machine only (bash and zsh, POSIX syntax). Not in git.\n"), 0o644)
					}
				default:
					return fmt.Errorf("unknown %q: shell, personal or local", args[0])
				}
			}
			return Editor(target)
		},
	}
}
