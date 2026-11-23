package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every command reminds of unpublished changes, except those whose output must stay as it is.
// (cobra adds its completion commands only when it runs, so the tree is built here.)
func TestRemind(t *testing.T) {
	root := &cobra.Command{Use: "nimble"}
	cmd := func(parent *cobra.Command, name string) *cobra.Command {
		c := &cobra.Command{Use: name}
		parent.AddCommand(c)
		return c
	}
	tool, completion := cmd(root, "tool"), cmd(root, "completion")
	for c, want := range map[*cobra.Command]bool{
		cmd(tool, "list"):                          true,
		cmd(root, "path"):                          true,
		cmd(root, "push"):                          true,
		cmd(root, "changes"):                       false, // ends with the same warning
		cmd(root, "help"):                          false,
		cmd(completion, "zsh"):                     false,
		cmd(root, cobra.ShellCompRequestCmd):       false, // the shell parses its output
		cmd(root, cobra.ShellCompNoDescRequestCmd): false,
	} {
		if got := remind(c); got != want {
			t.Errorf("remind(%s) = %v, want %v", c.CommandPath(), got, want)
		}
	}
	if remind(nil) {
		t.Error("remind(nil)")
	}
}
