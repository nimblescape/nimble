package setup

import "testing"

func TestGoToolBin(t *testing.T) {
	for pkg, want := range map[string]string{
		"golang.org/x/tools/gopls":                 "gopls",
		"github.com/go-delve/delve/cmd/dlv@v1.2.3": "dlv",
		"github.com/mikefarah/yq/v4":               "yq",
		"github.com/mikefarah/yq/v4@latest":        "yq",
		"example.com/tool":                         "tool",
	} {
		if got := GoToolBin(pkg); got != want {
			t.Errorf("GoToolBin(%s) = %s, want %s", pkg, got, want)
		}
	}
}
