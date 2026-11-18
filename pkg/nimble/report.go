package nimble

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrProblems makes nimble exit 1 after it has already reported why.
var ErrProblems = errors.New("problems found")

// colorEnabled is set if stdout is a terminal, respecting NO_COLOR.
var colorEnabled = IsTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""

func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Bold(s string) string   { return paint("1", s) }
func Red(s string) string    { return paint("31", s) }
func Green(s string) string  { return paint("32", s) }
func Yellow(s string) string { return paint("33", s) }
func Cyan(s string) string   { return paint("36", s) }

// Header starts a section of a report.
func Header(title string) { stopSpinner(); fmt.Printf("\n%s\n", Bold(title)) }

// Banner starts the output for one host.
func Banner(title string) {
	stopSpinner()
	fmt.Printf("\n%s\n", paint("1;36", "━━━ "+title+" ━━━"))
}

// Step announces the next step of a longer operation.
func Step(msg string) { stopSpinner(); fmt.Printf("%s %s\n", Bold("---"), msg) }

// Info prints a label and a value.
func Info(label, value string) { stopSpinner(); fmt.Printf("  %-24s %s\n", label, value) }

// Report collects check results and counts the problems.
type Report struct{ Problems int }

func (r *Report) line(mark, label, msg string) {
	stopSpinner()
	fmt.Printf("  %s %-22s %s\n", mark, label, msg)
}

// OK reports a passed check.
func (r *Report) OK(label, msg string) { r.line(Green("✓"), label, msg) }

// Bad reports a problem.
func (r *Report) Bad(label, msg string) {
	r.Problems++
	r.line(Red("✗"), label, msg)
}

// Note reports something worth knowing that isn't a problem.
func (r *Report) Note(label, msg string) { r.line(Yellow("•"), label, msg) }

// Same compares a pinned version with what is installed.
func (r *Report) Same(label, pinned, installed string) {
	switch {
	case installed == pinned:
		r.OK(label, installed)
	case installed == "":
		r.Bad(label, "missing (pinned "+pinned+")")
	default:
		r.Bad(label, installed+" (pinned "+pinned+")")
	}
}

// Present reports whether all names are installed.
func (r *Report) Present(label string, names []string, has func(string) bool) {
	var missing []string
	for _, n := range names {
		if !has(n) {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		r.OK(label, fmt.Sprintf("%d/%d installed", len(names), len(names)))
	} else {
		r.Bad(label, "missing: "+strings.Join(missing, ", "))
	}
}

// Done prints the summary; it returns ErrProblems if anything was wrong.
func (r *Report) Done(fix string) error {
	if r.Problems == 0 {
		fmt.Printf("\n%s\n", Green("All good."))
		return nil
	}
	fmt.Printf("\n%s — %s\n", Red(fmt.Sprintf("%d problem(s)", r.Problems)), fix)
	return ErrProblems
}
