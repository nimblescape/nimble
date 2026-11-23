package keyval

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestComplete(t *testing.T) {
	settings := func(name string) []Setting {
		return []Setting{
			{Key: "address", Help: "hostname", Value: "10.0.0.5"},
			{Key: "jump", Help: "through", Choices: []Choice{{Value: "", Desc: "directly"}, {Value: "swan", Desc: "s"}}},
			{Key: "reach", Help: "entries", List: true, Choices: []Choice{{Value: "all", Desc: "every", Alone: true}, {Value: "a", Desc: "A"}, {Value: "b", Desc: "B"}}},
		}
	}
	complete := Complete(func() []string { return []string{"grizzly", "swan"} }, settings)
	run := func(toComplete string, args ...string) ([]string, cobra.ShellCompDirective) {
		return complete(nil, args, toComplete)
	}
	check := func(got []string, want ...string) {
		t.Helper()
		if len(want) == 0 {
			want = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	got, _ := run("")
	check(got, "grizzly", "swan")
	got, d := run("", "grizzly")
	check(got, "address=\thostname", "jump=\tthrough", "reach=\tentries")
	if d&cobra.ShellCompDirectiveNoSpace == 0 {
		t.Error("a key= needs no space after it")
	}
	got, _ = run("", "grizzly", "jump=swan") // a key given already isn't offered again
	check(got, "address=\thostname", "reach=\tentries")
	got, _ = run("j", "grizzly")
	check(got, "jump=\tthrough")
	got, d = run("jump=", "grizzly") // "" (remove) isn't a completion: key= is typed as it is
	check(got, "jump=swan\ts")
	if d&cobra.ShellCompDirectiveNoSpace != 0 {
		t.Error("a single value ends the word")
	}
	got, _ = run("address=", "grizzly") // free text: the current value, to edit
	check(got, "address=10.0.0.5\tcurrent")
	got, d = run("reach=", "grizzly")
	check(got, "reach=all\tevery", "reach=a\tA", "reach=b\tB")
	if d&cobra.ShellCompDirectiveNoSpace == 0 {
		t.Error("a list goes on after a comma")
	}
	got, _ = run("reach=a,", "grizzly") // after a comma: the rest, and no "all"
	check(got, "reach=a,b\tB")
	got, _ = run("nope=", "grizzly")
	check(got)
}

func TestCompleteFlag(t *testing.T) {
	f := CompleteFlag("reach", func() []Setting {
		return []Setting{{Key: "reach", List: true, Choices: []Choice{{Value: "all", Alone: true}, {Value: "a"}, {Value: "b"}}}}
	})
	got, _ := f(nil, nil, "b,")
	if !reflect.DeepEqual(got, []string{"b,a\t"}) {
		t.Errorf("got %q", got)
	}
}
