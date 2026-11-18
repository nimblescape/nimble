package template

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/nimblescape/nimble/cmd/repo"
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
	"github.com/spf13/cobra"
)

func NewCmdTemplate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template <command>",
		Short: "Manage the templates: which tools and shell a machine gets (templates/*.yaml)",
		Long: heredoc.Doc(`
			A template is a YAML file in templates/: description, os (macos, debian or both),
			shell (zsh, bash or the system default) and the tools from the catalog. A machine
			uses its host file's template, else the default one from nimble.yaml; nothing is
			installed that no template lists.
		`),
	}
	cmd.AddCommand(newList(), newShow(), newCreate(), newSet(), newEdit(), newDefault(), newRemove())
	return cmd
}

func completeTemplates(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	ts, _ := config.Templates()
	var out []string
	for _, t := range ts {
		out = append(out, t.Name+"\t"+t.Description)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// check validates a template file: it parses, and its tools and settings are known.
func check(name string) error {
	for _, e := range config.Validate(setup.KnownTools()) {
		if strings.Contains(e.Error(), "templates/"+name+".yaml") {
			return e
		}
	}
	t, err := config.FindTemplate(name)
	if err != nil {
		return err
	}
	for _, tool := range t.Tools {
		if _, ok := setup.FindTool(tool); !ok {
			return fmt.Errorf("templates/%s.yaml: unknown tool %q (known: %s)", name, tool, strings.Join(setup.ToolNames(), ", "))
		}
	}
	if t.OS != "" && t.OS != "macos" && t.OS != "debian" {
		return fmt.Errorf("templates/%s.yaml: os must be macos or debian", name)
	}
	if t.Shell != "" && t.Shell != "zsh" && t.Shell != "bash" {
		return fmt.Errorf("templates/%s.yaml: shell must be zsh or bash", name)
	}
	return nil
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the templates, the default marked",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			ts, err := config.Templates()
			if err != nil {
				return err
			}
			for _, t := range ts {
				mark := " "
				if t.Name == c.DefaultTemplate {
					mark = nimble.Green("*")
				}
				os, shell := t.OS, t.Shell
				if os == "" {
					os = "both"
				}
				if shell == "" {
					shell = "default"
				}
				fmt.Printf("%s %-14s %-7s shell %-8s %s\n", mark, t.Name, os, shell, strings.Join(t.Tools, ","))
				if t.Description != "" {
					fmt.Printf("  %s\n", nimble.Bold("")+t.Description)
				}
				if users := config.TemplateUsers(c, t.Name); len(users) > 0 {
					fmt.Printf("  used by: %s\n", strings.Join(users, ", "))
				}
			}
			fmt.Println("\n* = default template (nimble template default <name>)")
			return nil
		},
	}
}

func newShow() *cobra.Command {
	return &cobra.Command{
		Use: "show <name>", Short: "Print a template's YAML", Args: cobra.MaximumNArgs(1), ValidArgsFunction: completeTemplates,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "template", templateNames())
			if err != nil {
				return err
			}
			b, err := os.ReadFile(config.TemplateFile(name))
			if err != nil {
				return fmt.Errorf("no template %s", name)
			}
			fmt.Print(string(b))
			return nil
		},
	}
}

func newCreate() *cobra.Command {
	var t config.Template
	var from, tools string
	var edit bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a template (from flags, a copy, or in your editor)",
		Example: heredoc.Doc(`
			nimble template create server --os debian --tools docker,starship --description "Docker host"
			nimble template create dev2 --from dev
		`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "template name", nil)
			if err != nil {
				return err
			}
			if from != "" {
				src, err := config.FindTemplate(from)
				if err != nil {
					return err
				}
				if t.Description == "" {
					t.Description = src.Description
				}
				if t.OS == "" {
					t.OS = src.OS
				}
				if t.Shell == "" {
					t.Shell = src.Shell
				}
				if tools == "" {
					t.Tools = src.Tools
				}
			}
			if tools != "" {
				for _, x := range strings.Split(tools, ",") {
					if x = strings.TrimSpace(x); x != "" {
						t.Tools = append(t.Tools, x)
					}
				}
			}
			t.Name = name
			if err := config.CreateTemplate(t); err != nil {
				return err
			}
			if edit {
				if err := repo.Editor(config.TemplateFile(name)); err != nil {
					return err
				}
			}
			if err := check(name); err != nil {
				_ = os.Remove(config.TemplateFile(name))
				return err
			}
			nimble.Say("wrote templates/" + name + ".yaml")
			return nimble.Record("templates: add "+name, "templates")
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "start from a copy of this template")
	cmd.Flags().StringVar(&tools, "tools", "", "tools from the catalog, comma-separated (nimble tool list)")
	cmd.Flags().StringVar(&t.Shell, "shell", "", "login shell: zsh or bash (default: the system's)")
	cmd.Flags().StringVar(&t.OS, "os", "", "limit to macos or debian (default: both)")
	cmd.Flags().StringVar(&t.Description, "description", "", "what the template is for")
	cmd.Flags().BoolVar(&edit, "edit", false, "open the new file in your editor")
	cmd.RegisterFlagCompletionFunc("from", completeTemplates)
	return cmd
}

func newSet() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> key=value…",
		Short: "Change a template (tools, shell, os, description)",
		Example: heredoc.Doc(`
			nimble template set dev tools=starship,node,go,python,rust
			nimble template set dev shell=bash
		`),
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeTemplates,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "template", templateNames())
			if err != nil {
				return err
			}
			settings := args[min(1, len(args)):]
			if len(settings) == 0 {
				s, err := nimble.Arg(nil, 0, "change (key=value; keys: tools, shell, os, description)", nil)
				if err != nil {
					return err
				}
				settings = []string{s}
			}
			before, _ := os.ReadFile(config.TemplateFile(name))
			for _, kv := range settings {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("%q: expected key=value", kv)
				}
				if err := config.SetTemplate(name, k, v); err != nil {
					return err
				}
			}
			if err := check(name); err != nil {
				_ = os.WriteFile(config.TemplateFile(name), before, 0o644) // keep the file valid
				return err
			}
			nimble.Say("updated templates/" + name + ".yaml")
			if err := nimble.Record("templates: "+name+" "+strings.Join(settings, " "), "templates"); err != nil {
				return err
			}
			nimble.Step("machines using it change on their next update: nimble update all")
			return nil
		},
	}
}

func newEdit() *cobra.Command {
	return &cobra.Command{
		Use: "edit <name>", Short: "Open a template's YAML in your editor (VS Code on a desktop) and check it", Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: completeTemplates,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "template", templateNames())
			if err != nil {
				return err
			}
			if err := repo.Editor(config.TemplateFile(name)); err != nil {
				return err
			}
			if err := check(name); err != nil {
				return err
			}
			if err := nimble.Record("templates: edit "+name, "templates"); err != nil {
				return err
			}
			nimble.Step("machines using it change on their next update: nimble update all")
			return nil
		},
	}
}

func newDefault() *cobra.Command {
	return &cobra.Command{
		Use: "default <name>", Short: "Make a template the default (machines without their own)", Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: completeTemplates,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "template", templateNames())
			if err != nil {
				return err
			}
			if err := config.SetDefaultTemplate(name); err != nil {
				return err
			}
			nimble.Say("default template: " + name)
			return nimble.Record("templates: default "+name, "nimble.yaml")
		},
	}
}

func newRemove() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>", Short: "Remove a template no machine uses", Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: completeTemplates,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := nimble.Arg(args, 0, "template", templateNames())
			if err != nil {
				return err
			}
			c, err := config.Load()
			if err != nil {
				return err
			}
			if name == c.DefaultTemplate {
				return errors.New(name + " is the default template — make another one the default first")
			}
			if users := config.TemplateUsers(c, name); len(users) > 0 {
				return errors.New(name + " is used by: " + strings.Join(users, ", "))
			}
			if err := os.Remove(config.TemplateFile(name)); err != nil {
				return fmt.Errorf("no template %s", name)
			}
			return nimble.Record("templates: remove "+name, "templates")
		},
	}
}

func templateNames() []string {
	ts, _ := config.Templates()
	var names []string
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return names
}
