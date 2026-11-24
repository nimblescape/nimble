package token

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/seal"
	"github.com/spf13/cobra"
)

// AgentSocket is where nimble agent listens by default: in a folder only its owner reaches.
func AgentSocket() string { return nimble.CacheDir("agent", "agent.sock") }

func NewCmdAgent() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "An ssh agent that also opens sealed tokens for the machines you ssh to",
		Long: heredoc.Doc(`
			Run an ssh agent in front of the one in SSH_AUTH_SOCK: it passes every ssh request
			(logins, sudo on the hosts) to that agent, and it answers requests to open a sealed
			token with the keys of this machine — after Touch ID here, or the PIN and a touch
			of a YubiKey when it runs in a terminal. Forward it, and a program on a host opens
			a token sealed for this Mac without the token or the key ever being on the host:

			  export SSH_AUTH_SOCK=~/.nimble/cache/agent/agent.sock   # in this shell
			  ssh swan                                                # ForwardAgent: managed hosts

			On the host, nimble token and every program that uses the package
			github.com/nimblescape/nimble/seal ask the forwarded agent when no key of the host
			opens the token. The agent shows each request here; Touch ID confirms each one. A
			forwarded agent reaches only the processes of that ssh login, while it lasts.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if socket == "" {
				socket = AgentSocket()
			}
			socket = expand(socket)
			upstream := os.Getenv("SSH_AUTH_SOCK")
			if abs(upstream) == abs(socket) {
				return errors.New("SSH_AUTH_SOCK is this agent's own socket — start it with the agent that holds the ssh keys in SSH_AUTH_SOCK")
			}
			if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
				return err
			}
			if err := os.Chmod(filepath.Dir(socket), 0o700); err != nil {
				return err
			}
			if c, err := net.DialTimeout("unix", socket, time.Second); err == nil {
				c.Close()
				return errors.New("an agent listens at " + nimble.Tilde(socket) + " already")
			}
			_ = os.Remove(socket) // left by an agent that ended without cleaning up
			l, err := net.Listen("unix", socket)
			if err != nil {
				return err
			}
			if err := os.Chmod(socket, 0o600); err != nil {
				l.Close()
				return err
			}
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			go func() {
				<-sig
				l.Close()
			}()
			defer os.Remove(socket)

			log := func(s string) { fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("15:04:05"), s) }
			opt := seal.AgentOptions{Upstream: upstream, FIDO2: nimble.Interactive(), Log: log}
			nimble.Say("nimble agent at " + nimble.Tilde(socket))
			if upstream != "" {
				nimble.Step("ssh requests go to " + nimble.Tilde(upstream))
			} else {
				nimble.Warn("SSH_AUTH_SOCK is not set: the agent opens tokens, but answers no ssh request")
			}
			nimble.Step("use it: export SSH_AUTH_SOCK=" + nimble.Tilde(socket) + " — Ctrl+C ends it")
			for {
				c, err := l.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return nil
					}
					return err
				}
				go func() {
					if err := seal.ServeAgent(c, opt); err != nil {
						log("connection: " + err.Error())
					}
				}()
			}
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "the socket to listen at (default ~/.nimble/cache/agent/agent.sock)")
	return cmd
}

func abs(p string) string {
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
