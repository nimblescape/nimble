package nimble

import (
	"fmt"
	"os/exec"
	"strings"
)

// GitHubRepo checks that owner/name exists on GitHub: with gh (private repos too, as you),
// else through the public API (public repos only). host/owner/name elsewhere isn't checked.
func GitHubRepo(repo string) error {
	parts := strings.Split(repo, "/")
	switch {
	case len(parts) == 3 && strings.Contains(parts[0], "."):
		return nil // another git host
	case len(parts) != 2 || parts[0] == "" || parts[1] == "":
		return fmt.Errorf("%q: expected owner/name", repo)
	}
	if _, err := exec.LookPath("gh"); err == nil {
		if exec.Command("gh", "api", "repos/"+repo, "--silent").Run() == nil {
			return nil
		}
		if exec.Command("gh", "auth", "status").Run() == nil {
			return fmt.Errorf("%s doesn't exist on GitHub, or you have no access to it", repo)
		}
	}
	if URLExists("https://api.github.com/repos/" + repo) {
		return nil
	}
	return fmt.Errorf("%s not found on GitHub — check the name; for a private repo log in first: gh auth login", repo)
}

// RepoName turns a git remote URL into owner/name (host/owner/name for hosts other than GitHub).
func RepoName(url string) string {
	u := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(url), "/"), ".git")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:] // ssh://git@github.com/o/n, https://github.com/o/n
	} else if i := strings.Index(u, ":"); i >= 0 && !strings.Contains(u[:i], "/") {
		u = u[:i] + "/" + u[i+1:] // git@github.com:o/n
	}
	if i := strings.Index(u, "@"); i >= 0 && i < strings.Index(u+"/", "/") {
		u = u[i+1:]
	}
	host, path, _ := strings.Cut(u, "/")
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // a port
	}
	if host == "github.com" {
		return path
	}
	return host + "/" + path
}
