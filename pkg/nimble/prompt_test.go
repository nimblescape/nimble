package nimble

import (
	"errors"
	"os"
	"testing"

	"github.com/AlecAivazis/survey/v2/terminal"
)

func TestCancelled(t *testing.T) {
	if err := cancelled(nil); err != nil {
		t.Errorf("cancelled(nil) = %v, want nil", err)
	}
	exitOnCancel(nil) // an answered prompt: must neither panic nor exit
	if err := cancelled(terminal.InterruptErr); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancelled(InterruptErr) = %v, want ErrCancelled", err)
	}
	other := errors.New("boom")
	if err := cancelled(other); err != other {
		t.Errorf("cancelled(other) = %v, want it unchanged", err)
	}
}

func TestEscReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	in := escReader{r}
	read := func(sent string) string {
		t.Helper()
		if _, err := w.Write([]byte(sent)); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 16)
		n, err := in.Read(b)
		if err != nil {
			t.Fatal(err)
		}
		return string(b[:n])
	}
	if got := read("\x1b"); got != "\x03" {
		t.Errorf("a lone Esc reads as %q, want Ctrl+C", got)
	}
	if got := read("\x1b[A"); got != "\x1b[A" {
		t.Errorf("arrow up reads as %q", got)
	}
	if got := read("q"); got != "q" {
		t.Errorf("q reads as %q", got)
	}
}
