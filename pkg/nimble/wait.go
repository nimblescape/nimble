package nimble

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// A wait is a step nimble runs without asking or printing anything (a download, a build, an
// install). On a terminal a spinner turns in front of its line until the line ends with ✓,
// or ✗ on an error, and the time it took:
//
//	⠹ send nimble          (while it runs)
//	✓ send nimble  2.1s    (when it's done)
//
// Anything that prints in the meantime — nimble's own lines and prompts, a command's output,
// sudo about to ask for a password — first stops the spinner and leaves a plain step line
// ("--- send nimble"), so nothing is drawn over a prompt.

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinOut is where the spinner draws; spinEnabled whether it may (tests change both).
var (
	spinOut     io.Writer = os.Stdout
	spinEnabled           = IsTerminal(os.Stdout) && IsTerminal(os.Stderr) && os.Getenv("TERM") != "dumb"
)

type spinner struct {
	msg     string
	start   time.Time
	mu      sync.Mutex
	stopped bool // the line has ended: as a plain step line, or with ✓/✗
	quit    chan struct{}
	done    chan struct{}
}

var (
	spinMu sync.Mutex
	spin   *spinner // the running spinner, if any
)

// Wait runs fn under the step line msg, with a spinner on a terminal (see above). fn must
// not need the terminal for itself; without one, Wait prints msg as Step does.
func Wait(msg string, fn func() error) error {
	if !spinEnabled {
		Step(msg)
		return fn()
	}
	stopSpinner() // a wait inside a wait: the outer one's line ends plainly
	s := &spinner{msg: msg, start: time.Now(), quit: make(chan struct{}), done: make(chan struct{})}
	spinMu.Lock()
	spin = s
	spinMu.Unlock()
	go s.turn()

	err := fn()

	spinMu.Lock()
	if spin == s {
		spin = nil
	}
	spinMu.Unlock()
	close(s.quit)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		mark := Green("✓")
		if err != nil {
			mark = Red("✗")
		}
		fmt.Fprintf(spinOut, "\r\033[K%s %s  %s\n", mark, msg, paint("2", duration(time.Since(s.start))))
	}
	return err
}

// turn draws the spinner until the wait ends or something else prints.
func (s *spinner) turn() {
	defer close(s.done)
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		fmt.Fprintf(spinOut, "\r%s %s\033[K", Cyan(spinFrames[i%len(spinFrames)]), fit(s.msg))
		s.mu.Unlock()
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
	}
}

// stopSpinner ends a running spinner's line as a plain step line, before something else
// prints. Every line nimble prints and every prompt calls it.
func stopSpinner() {
	spinMu.Lock()
	s := spin
	spin = nil
	spinMu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		fmt.Fprintf(spinOut, "\r\033[K%s %s\n", Bold("---"), s.msg)
	}
}

func spinning() bool {
	spinMu.Lock()
	defer spinMu.Unlock()
	return spin != nil
}

// fit shortens msg to the terminal's width: a line that wraps can't be redrawn in place.
func fit(msg string) string {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 10 {
		return msg
	}
	if max := w - 3; utf8.RuneCountInString(msg) > max { // "⠹ " in front, the cursor behind
		return string([]rune(msg)[:max-1]) + "…"
	}
	return msg
}

func duration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// stopFirst is a command's output while a spinner turns: its first byte stops the spinner.
type stopFirst struct{ w io.Writer }

func (s stopFirst) Write(b []byte) (int, error) {
	stopSpinner()
	return s.w.Write(b)
}

// Stdout and Stderr are where a command's output goes: the terminal itself, or, while a
// spinner turns, a writer that stops it first.
func Stdout() io.Writer { return outTo(os.Stdout) }
func Stderr() io.Writer { return outTo(os.Stderr) }

func outTo(f *os.File) io.Writer {
	if spinning() {
		return stopFirst{f}
	}
	return f
}

// Exec runs a command whose output may go to Stdout and Stderr. Those are pipes while a
// spinner turns, and a background child that keeps them open (an ssh ControlPersist master)
// would make Run wait for it: WaitDelay lets go of them once the command itself has ended.
func Exec(cmd *exec.Cmd) error {
	if spinning() {
		cmd.WaitDelay = time.Second
	}
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil // the command succeeded; only a background child still held its output
	}
	return err
}

// ExecOutput is Exec for a command whose stdout nimble reads.
func ExecOutput(cmd *exec.Cmd) (string, error) {
	var out strings.Builder
	cmd.Stdout = &out
	err := Exec(cmd)
	return out.String(), err
}
