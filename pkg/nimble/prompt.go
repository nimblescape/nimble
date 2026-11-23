package nimble

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
)

// Prompts use survey, like the GitHub CLI's: an editable default, a validator that asks again,
// arrow keys and type-to-filter for lists. Without a terminal, Ask and Confirm read a line
// from stdin (scripts can pipe answers) and the Arg helpers fail on a missing value.

var stdin = bufio.NewReader(os.Stdin)

// escReader is the terminal for prompts. survey's lists take Esc as a switch to vim keys,
// not as cancel; a lone Esc (a read of that one byte — an arrow key's sequence arrives in
// one read, which survey relies on too) becomes Ctrl+C, so Esc cancels every prompt.
type escReader struct{ *os.File }

func (r escReader) Read(p []byte) (int, error) {
	n, err := r.File.Read(p)
	if n == 1 && p[0] == 0x1b {
		p[0] = 0x03
	}
	return n, err
}

var promptIO = survey.WithStdio(escReader{os.Stdin}, os.Stdout, os.Stderr)

// ErrCancelled is returned when the user leaves a prompt with Ctrl+C or ESC.
var ErrCancelled = errors.New("cancelled")

func cancelled(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, terminal.InterruptErr) || err.Error() == "interrupt" || strings.Contains(err.Error(), "escape sequence") {
		return ErrCancelled
	}
	return err
}

// exitOnCancel ends nimble when a prompt without an error return is cancelled: Esc (or
// Ctrl+C) is a graceful exit, status 0.
func exitOnCancel(err error) {
	if errors.Is(cancelled(err), ErrCancelled) {
		fmt.Println()
		os.Exit(0)
	}
}

// Interactive reports whether nimble can ask questions: stdin is a terminal.
func Interactive() bool { return IsTerminal(os.Stdin) }

// Input asks for a value with an editable default until validate (if any) accepts it.
func Input(message, def string, validate func(string) error) (string, error) {
	stopSpinner()
	var v string
	opts := []survey.AskOpt{promptIO}
	if validate != nil {
		opts = append(opts, survey.WithValidator(func(ans interface{}) error { return validate(strings.TrimSpace(ans.(string))) }))
	}
	if err := survey.AskOne(&survey.Input{Message: message, Default: def}, &v, opts...); err != nil {
		return "", cancelled(err)
	}
	return strings.TrimSpace(v), nil
}

// Secret asks for a value that isn't shown while it is typed or pasted (a token).
func Secret(message string) (string, error) {
	stopSpinner()
	var v string
	err := survey.AskOne(&survey.Password{Message: message}, &v, survey.WithValidator(func(ans interface{}) error {
		return notEmpty(strings.TrimSpace(ans.(string)))
	}), promptIO)
	if err != nil {
		return "", cancelled(err)
	}
	return strings.TrimSpace(v), nil
}

// Select picks one of options.
func Select(message string, options []string, def string) (string, error) {
	stopSpinner()
	var v string
	p := &survey.Select{Message: message, Options: options, PageSize: 20}
	for _, o := range options {
		if o == def {
			p.Default = def
		}
	}
	if err := survey.AskOne(p, &v, promptIO); err != nil {
		return "", cancelled(err)
	}
	return v, nil
}

// MultiSelect picks at least one of options; defaults are preselected.
func MultiSelect(message string, options, defaults []string) ([]string, error) {
	stopSpinner()
	var v []string
	err := survey.AskOne(&survey.MultiSelect{Message: message, Options: options, Default: defaults, PageSize: 20},
		&v, survey.WithValidator(survey.MinItems(1)), promptIO)
	if err != nil {
		return nil, cancelled(err)
	}
	return v, nil
}

// Checklist is MultiSelect where checking nothing is an answer too (everything unchecked).
func Checklist(message string, options, checked []string) ([]string, error) {
	stopSpinner()
	v := []string{}
	err := survey.AskOne(&survey.MultiSelect{Message: message, Options: options, Default: checked, PageSize: 20}, &v, promptIO)
	if err != nil {
		return nil, cancelled(err)
	}
	return v, nil
}

// Ask asks a question; an empty answer is def.
func Ask(question, def string) string {
	stopSpinner()
	if Interactive() {
		v, err := Input(question, def, nil)
		exitOnCancel(err)
		if v == "" {
			return def
		}
		return v
	}
	fmt.Printf("%s %s ", Cyan("?"), question)
	if def != "" {
		fmt.Printf("[%s] ", def)
	}
	line, _ := stdin.ReadString('\n')
	if line = strings.TrimSpace(line); line == "" {
		return def
	}
	return line
}

// Confirm asks a yes/no question; Esc or Ctrl+C ends nimble gracefully.
func Confirm(question string, def bool) bool {
	stopSpinner()
	if Interactive() {
		v, err := ConfirmOrCancel(question, def)
		exitOnCancel(err)
		return v
	}
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	switch strings.ToLower(Ask(question+" ("+hint+")", "")) {
	case "y", "yes", "j", "ja":
		return true
	case "n", "no", "nein":
		return false
	}
	return def
}

// ConfirmOrCancel asks a yes/no question on the terminal and returns ErrCancelled on Esc or
// Ctrl+C, for a caller that has something to undo first.
func ConfirmOrCancel(question string, def bool) (bool, error) {
	stopSpinner()
	v := def
	err := survey.AskOne(&survey.Confirm{Message: question, Default: def}, &v, promptIO)
	return v, cancelled(err)
}

// Pause shows an instruction and waits for Enter.
func Pause(instruction string) {
	stopSpinner()
	fmt.Printf("%s %s — then press Enter ", Cyan("→"), instruction)
	_, _ = stdin.ReadString('\n')
}

func notEmpty(v string) error {
	if v == "" {
		return errors.New("a value is needed")
	}
	return nil
}

// Arg returns args[i] or, when it is missing, asks for it on a terminal: every command takes
// its inputs from the command line and prompts for what's missing — a list to pick from when
// the choices are known. Without a terminal a missing value is an error.
func Arg(args []string, i int, what string, choices []string) (string, error) {
	if i < len(args) && strings.TrimSpace(args[i]) != "" {
		return args[i], nil
	}
	if !Interactive() {
		return "", fmt.Errorf("missing %s", what)
	}
	switch len(choices) {
	case 0:
		return Input(capital(what)+":", "", notEmpty)
	case 1:
		return Input(capital(what)+":", choices[0], notEmpty)
	}
	return Select("Select the "+what+" (ESC or Ctrl+C to cancel):", choices, "")
}

// ArgList is Arg for one or more values: a multi-select of the choices.
func ArgList(args []string, what string, choices []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	if !Interactive() {
		return nil, fmt.Errorf("missing %s", what)
	}
	return MultiSelect("Select the "+what+" (space selects, enter confirms):", choices, nil)
}

// ArgOr is Arg with a value nimble detected (e.g. the repo checked out in the current
// folder): on a terminal it is the editable default, and the answer must pass validate;
// without a terminal the detected value is used and said. A given or detected value is
// validated too.
func ArgOr(args []string, i int, what, detected, source string, validate func(string) error) (string, error) {
	check := func(v string) error {
		if err := notEmpty(v); err != nil {
			return err
		}
		if validate != nil {
			return validate(v)
		}
		return nil
	}
	if i < len(args) && strings.TrimSpace(args[i]) != "" {
		return args[i], check(args[i])
	}
	if !Interactive() {
		if detected == "" {
			return "", fmt.Errorf("missing %s", what)
		}
		Say(what + ": " + detected + " (" + source + ")")
		return detected, check(detected)
	}
	msg := capital(what) + ":"
	if detected != "" {
		msg = capital(what) + " (" + source + "):"
	}
	return Input(msg, detected, check)
}

func capital(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
