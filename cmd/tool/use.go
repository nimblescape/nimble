package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/releases"
	"github.com/nimblescape/nimble/pkg/versions"
	"github.com/spf13/cobra"
)

// newUse is tool use: pin a language or npm package to a version picked from its releases.
func newUse() *cobra.Command {
	return &cobra.Command{
		Use:   "use [tool] [version]",
		Short: "Pin a language or npm package to a version picked from its releases",
		Long: heredoc.Doc(`
			Choose the version of a language every machine uses, the way nvm chooses Node: pick the
			language and the version from lists (current, bugfix and feature updates marked, and
			Node's LTS releases), or name them. It writes toolchain/pins.yaml; nimble update then
			installs it with the language's own tool — nvm for Node, npm for the global npm
			packages, go.dev's release in ~/sdk for Go, rustup for Rust, uv for Python — and
			nimble push shares the pin.
		`),
		Example: heredoc.Doc(`
			nimble tool use                # pick a language, then its version
			nimble tool use node           # pick Node's version (LTS releases marked)
			nimble tool use go 1.28        # the newest 1.28.x
			nimble tool use node 26.13.1
		`),
		Args: cobra.MaximumNArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			p, err := config.LoadPins()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var out []string
			for _, e := range versions.Selectable(p) {
				out = append(out, e.Label+"\t"+show(e.Current)+" ("+e.Installer+")")
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.LoadPins()
			if err != nil {
				return err
			}
			es := versions.Selectable(p)
			name, err := nimble.Arg(args, 0, "language", versions.Labels(es))
			if err != nil {
				return err
			}
			e, ok := versions.Find(es, name)
			if !ok {
				return fmt.Errorf("no pin %q — known: %s", name, strings.Join(versions.Labels(es), ", "))
			}
			var raw []string
			if err := nimble.Wait("releases of "+e.Label+" ("+e.Installer+")", func() (err error) {
				raw, err = e.Source()
				return err
			}); err != nil {
				return err
			}
			list := releases.Sorted(raw)
			if len(list) == 0 {
				return errors.New("no releases found for " + e.Label)
			}

			var want string
			if len(args) > 1 && args[1] == "latest" && e.Key == "npmGlobal" {
				want = "latest" // an npm package can follow its newest release
			} else if len(args) > 1 {
				if want = match(list, args[1]); want == "" {
					return fmt.Errorf("%s has no release %s", e.Label, args[1])
				}
			} else if want, err = pick(e, list); err != nil {
				return err
			}

			if want == e.Current || (want == "latest" && e.Current == "") {
				nimble.Say(e.Label + " is pinned to " + show(e.Current) + " already")
				return nil
			}
			if want == "latest" {
				want = ""
			}
			if err := versions.Write(e, want); err != nil {
				return err
			}
			nimble.Say(fmt.Sprintf("%s: %s → %s in toolchain/pins.yaml", e.Label, show(e.Current), show(want)))
			nimble.Step("install it: nimble update · share it: nimble push · hosts: nimble update all")
			return nil
		},
	}
}

// match finds a release by its exact version or a prefix (1.28 → the newest 1.28.x).
func match(list []string, v string) string {
	v = strings.TrimPrefix(v, "v")
	for _, r := range list { // newest first
		if r == v || strings.HasPrefix(r, v+".") {
			return r
		}
	}
	return ""
}

// pick shows the releases newest first, the current one, the bugfix and feature updates marked.
func pick(e versions.Entry, list []string) (string, error) {
	bugfix, feature := "", ""
	if e.Current != "" {
		bugfix = releases.NewestCompatible(e.Current, list, e.Level)
		feature = releases.NewestFeature(e.Current, list, e.Level)
	}
	var options []string
	label := map[string]string{}
	if e.Key == "npmGlobal" {
		o := "latest — follow the newest release"
		if e.Current == "" {
			o += "  (current)"
		}
		options, label[o] = append(options, o), "latest"
	}
	marks := map[string]string{}
	if e.Marks != nil {
		marks, _ = e.Marks() // only extra notes: the list works without them
	}
	def := ""
	for _, v := range list {
		var notes []string
		switch v {
		case e.Current:
			notes = append(notes, "current")
		case bugfix:
			notes = append(notes, "bugfix update")
		case feature:
			notes = append(notes, "feature update")
		}
		if m := marks[v]; m != "" {
			notes = append(notes, m)
		}
		o := v
		if len(notes) > 0 {
			o += "  (" + strings.Join(notes, ", ") + ")"
		}
		options, label[o] = append(options, o), v
		if v == e.Current {
			def = o
		}
	}
	if def == "" && len(options) > 0 {
		def = options[0]
	}
	chosen, err := nimble.Select("Version of "+e.Label+" (type to filter, ESC or Ctrl+C to cancel):", options, def)
	if err != nil {
		return "", err
	}
	return label[chosen], nil
}

func show(v string) string {
	if v == "" {
		return "latest"
	}
	return v
}
