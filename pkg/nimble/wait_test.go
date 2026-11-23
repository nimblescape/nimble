package nimble

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is the spinner's output in tests (it draws from its own goroutine).
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func spinTo(t *testing.T) *syncBuffer {
	t.Helper()
	out := &syncBuffer{}
	oldOut, oldEnabled := spinOut, spinEnabled
	spinOut, spinEnabled = out, true
	t.Cleanup(func() { spinOut, spinEnabled = oldOut, oldEnabled })
	return out
}

func TestWait(t *testing.T) {
	t.Run("done: the spinner turns, then ✓ and the time", func(t *testing.T) {
		out := spinTo(t)
		if err := Wait("send nimble", func() error { time.Sleep(250 * time.Millisecond); return nil }); err != nil {
			t.Fatal(err)
		}
		s := out.String()
		if !strings.Contains(s, "⠋ send nimble") || !regexp.MustCompile(`\r\x1b\[K✓ send nimble  0\.[2-9]s\n$`).MatchString(s) {
			t.Errorf("%q", s)
		}
		if spinning() {
			t.Error("still spinning")
		}
	})

	t.Run("failed: ✗, and the error comes back", func(t *testing.T) {
		out := spinTo(t)
		boom := errors.New("boom")
		if err := Wait("send nimble", func() error { return boom }); err != boom {
			t.Fatalf("err %v", err)
		}
		if !strings.Contains(out.String(), "✗ send nimble") {
			t.Errorf("%q", out.String())
		}
	})

	t.Run("something prints: a plain step line, no ✓ drawn over it", func(t *testing.T) {
		out := spinTo(t)
		_ = Wait("send nimble", func() error {
			stopSpinner() // what Say, Warn, a prompt or a command's output do first
			time.Sleep(100 * time.Millisecond)
			return nil
		})
		s := out.String()
		if !strings.HasSuffix(s, "\r\x1b[K--- send nimble\n") || strings.Contains(s, "✓") {
			t.Errorf("%q", s)
		}
	})

	t.Run("a command's output stops the spinner first", func(t *testing.T) {
		out := spinTo(t)
		_ = Wait("install", Cmd{Name: "sh", Args: []string{"-c", "echo from the command >&2"}}.Do)
		if !strings.Contains(out.String(), "--- install\n") {
			t.Errorf("%q", out.String())
		}
	})

	t.Run("a quiet command keeps the spinner", func(t *testing.T) {
		out := spinTo(t)
		_ = Wait("install", Cmd{Name: "sh", Args: []string{"-c", "echo discarded"}, Quiet: true}.Do)
		if !strings.Contains(out.String(), "✓ install") {
			t.Errorf("%q", out.String())
		}
	})

	t.Run("a background child holding the output (an ssh master) doesn't hold up the step", func(t *testing.T) {
		spinTo(t)
		start := time.Now()
		err := Wait("send nimble", Cmd{Name: "sh", Args: []string{"-c", "sleep 4 & echo started"}}.Do)
		if err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("waited %s for the background child", d)
		}
	})

	t.Run("without a terminal: just fn", func(t *testing.T) {
		out := spinTo(t)
		spinEnabled = false
		ran := false
		_ = Wait("send nimble", func() error { ran = true; return nil })
		if !ran || out.String() != "" {
			t.Errorf("ran %v, drew %q", ran, out.String())
		}
	})
}
