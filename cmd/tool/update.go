package tool

import (
	"fmt"
	"strings"
	"sync"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/releases"
	"github.com/nimblescape/nimble/pkg/versions"
	"github.com/spf13/cobra"
)

// newUpdate is tool update: write updates of the pinned versions into pins.yaml.
func newUpdate() *cobra.Command {
	var markdown, feature bool
	cmd := &cobra.Command{
		Use:   "update [name…]",
		Short: "Write updates into toolchain/pins.yaml — all bugfix updates, or the ones you pick",
		Long: heredoc.Doc(`
			Write the updates nimble tool list shows into pins.yaml. Bugfix updates follow each
			language's own scheme for fixes that keep compatibility: the patch release (x.y.Z)
			for Go, Python and Node; the 1.x releases for Rust, which ships its
			fixes there under its stability promise. Anything newer is a feature update.

			On a terminal it shows a checklist: bugfix updates checked, feature updates not;
			pick what you want. With names, only those pins (--feature: their feature update
			instead). Without a terminal (the daily workflow) every bugfix update.

			It only writes pins.yaml: nimble update installs the new versions here, nimble
			update all on the hosts, and nimble push shares the pins.
		`),
		Example: heredoc.Doc(`
			nimble tool update                  # pick from a checklist
			nimble tool update go node          # just these, bugfix updates
			nimble tool update python --feature # python's feature update (a new minor)
		`),
		RunE: func(cmd *cobra.Command, args []string) error { return check(true, markdown, args, feature) },
	}
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print a Markdown table (for pull requests)")
	cmd.Flags().BoolVar(&feature, "feature", false, "with names: take their feature update")
	return cmd
}

type pin struct {
	key, label, current, latest, feature, err string
}

func check(write, markdown bool, names []string, feature bool) error {
	p, err := config.LoadPins()
	if err != nil {
		return err
	}
	// look every release list up at once: tool list runs this too, and should stay quick
	type lookup struct {
		list []string
		err  error
	}
	var entries []versions.Entry
	lookups := map[string]*lookup{}
	var wg sync.WaitGroup
	for _, e := range versions.Entries(p) {
		if e.Current == "" {
			continue // not pinned: follows the newest release anyway
		}
		entries = append(entries, e)
		if id := e.Installer + ":" + e.Label; lookups[id] == nil {
			l := &lookup{}
			lookups[id] = l
			wg.Add(1)
			go func(source func() ([]string, error)) {
				defer wg.Done()
				l.list, l.err = source()
			}(e.Source)
		}
	}
	if markdown {
		wg.Wait() // the daily workflow: what it prints is the pull request's text
	} else {
		_ = nimble.Wait("look up the newest releases", func() error { // a failed lookup: ✗ here, "could not check" below
			wg.Wait()
			for _, l := range lookups {
				if l.err != nil {
					return l.err
				}
			}
			return nil
		})
	}
	var pins []pin
	for _, e := range entries {
		l := lookups[e.Installer+":"+e.Label]
		x := pin{key: e.Key, label: e.Label, current: e.Current}
		if l.err != nil {
			x.err = l.err.Error()
		} else {
			x.latest = releases.NewestCompatible(e.Current, l.list, e.Level)
			x.feature = releases.NewestFeature(e.Current, l.list, e.Level)
		}
		pins = append(pins, x)
	}

	updates, features, failed := 0, 0, 0
	for _, x := range pins {
		if x.err != "" {
			failed++
		}
		if x.feature != "" {
			features++
		}
		if x.latest != "" {
			updates++
		}
	}
	written := map[int]string{}
	if write {
		pick, err := choose(pins, names, feature, markdown)
		if err != nil {
			return err
		}
		for i, v := range pick {
			if err := config.ReplaceVersion(pins[i].key, pins[i].current, v); err != nil {
				return err
			}
			written[i] = v
		}
		updates = len(written)
	}
	if markdown {
		printMarkdown(pins, updates, features, failed)
		return nil
	}
	for i, x := range pins {
		feature := ""
		if x.feature != "" {
			feature = nimble.Yellow("   feature update: " + x.feature)
		}
		switch {
		case x.err != "":
			fmt.Printf("  %s %-30s %-9s could not check: %s\n", nimble.Yellow("?"), x.label, x.current, x.err)
		case written[i] != "":
			fmt.Printf("  %s %-30s %-9s → %s (written)\n", nimble.Green("✓"), x.label, x.current, nimble.Bold(written[i]))
		case x.latest != "":
			fmt.Printf("  %s %-30s %-9s → %s%s\n", nimble.Cyan("↑"), x.label, x.current, nimble.Bold(x.latest), feature)
		default:
			fmt.Printf("  %s %-30s %-9s up to date%s\n", nimble.Green("✓"), x.label, x.current, feature)
		}
	}
	fmt.Println()
	switch {
	case write && updates == 0:
		fmt.Println("Nothing written.")
	case updates == 0:
		fmt.Println(nimble.Green("No bugfix updates."))
	case write:
		fmt.Printf("%s — install: nimble update · commit and share: nimble push\n", nimble.Bold(fmt.Sprintf("Updated %d pin(s) in toolchain/pins.yaml", updates)))
	default:
		fmt.Printf("%s — nimble tool update writes them\n", nimble.Bold(fmt.Sprintf("%d bugfix update(s) available", updates)))
	}
	if features > 0 {
		fmt.Printf("%d feature update(s) available — read the release notes, then: nimble tool update (checklist) or nimble tool update <name> --feature\n", features)
	}
	if failed > 0 {
		fmt.Printf("%s\n", nimble.Yellow(fmt.Sprintf("%d pin(s) could not be checked — try again later", failed)))
	}
	return nil
}

// printMarkdown prints the table for the daily workflow. Its markers tell the workflow
// what it found: feature updates, and lookups that failed (then "no feature updates" isn't
// known, and the workflow leaves its issue alone).
func printMarkdown(pins []pin, updates, features, failed int) {
	if updates == 0 && features == 0 && failed == 0 {
		fmt.Println("All pinned versions are up to date.")
		return
	}
	var b strings.Builder
	if features > 0 {
		b.WriteString("<!-- feature-updates -->\n")
	}
	if failed > 0 {
		b.WriteString("<!-- incomplete -->\n")
	}
	b.WriteString("Updates for the pinned versions. Bugfix updates follow each tool's own scheme (the patch release; Rust's 1.x releases); feature updates are only reported.\n\n| | pinned | bugfix update | feature update |\n|---|---|---|---|\n")
	for _, x := range pins {
		bugfix, feature := "up to date", x.feature
		switch {
		case x.err != "":
			bugfix = "could not check"
		case x.latest != "":
			bugfix = "**" + x.latest + "**"
		}
		if feature == "" {
			feature = "–"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", x.label, x.current, bugfix, feature)
	}
	if updates > 0 {
		b.WriteString("\nAfter merging the bugfix updates: `nimble update` on the workstation, then `nimble update all` for the hosts.")
	}
	if features > 0 {
		b.WriteString("\nFeature updates: read the release notes, then pick them with `nimble tool update` (a checklist) or `nimble tool update <name> --feature`, and `nimble push`.")
	}
	fmt.Println(b.String())
}

// choose decides which updates tool update writes (pin index → version): the named pins, a
// checklist on a terminal (bugfix updates checked, feature updates not), else every bugfix
// update (the daily workflow).
func choose(pins []pin, names []string, feature, markdown bool) (map[int]string, error) {
	pick := map[int]string{}
	if len(names) > 0 {
		for _, n := range names {
			found := false
			for i, x := range pins {
				if n != x.key && n != x.label {
					continue
				}
				found = true
				switch {
				case feature && x.feature != "":
					pick[i] = x.feature
				case feature:
					nimble.Say(x.label + ": no feature update (nothing newer than its " + x.current + " line)")
				case x.latest != "":
					pick[i] = x.latest
				default:
					nimble.Say(x.label + ": no bugfix update")
				}
			}
			if !found {
				return nil, fmt.Errorf("no pin %q (nimble tool list shows them)", n)
			}
		}
		return pick, nil
	}
	if markdown || !nimble.Interactive() {
		for i, x := range pins {
			if x.latest != "" {
				pick[i] = x.latest
			}
		}
		return pick, nil
	}
	type option struct {
		i int
		v string
	}
	var labels, defaults []string
	opts := map[string]option{}
	for i, x := range pins {
		if x.latest != "" {
			l := fmt.Sprintf("%s %s → %s (bugfix)", x.label, x.current, x.latest)
			labels, defaults, opts[l] = append(labels, l), append(defaults, l), option{i, x.latest}
		}
		if x.feature != "" {
			l := fmt.Sprintf("%s %s → %s (feature)", x.label, x.current, x.feature)
			labels, opts[l] = append(labels, l), option{i, x.feature}
		}
	}
	if len(labels) == 0 {
		return pick, nil
	}
	chosen, err := nimble.MultiSelect("Updates to write into toolchain/pins.yaml (space toggles, enter writes):", labels, defaults)
	if err != nil {
		return nil, err
	}
	for _, l := range chosen {
		o := opts[l]
		if cur, ok := pick[o.i]; !ok || releasesLess(cur, o.v) {
			pick[o.i] = o.v // bugfix and feature both picked: the feature update wins
		}
	}
	return pick, nil
}

func releasesLess(a, b string) bool {
	return releases.NewestCompatible(a, []string{b}, "minor") == b || releases.NewestFeature(a, []string{b}, "patch") == b
}
