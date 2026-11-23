package nimble

import "testing"

func TestRepoName(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:nimblescape/sportsweek.git":       "nimblescape/sportsweek",
		"https://github.com/nimblescape/sportsweek.git":   "nimblescape/sportsweek",
		"https://github.com/nimblescape/sportsweek":       "nimblescape/sportsweek",
		"ssh://git@github.com/nimblescape/sportsweek.git": "nimblescape/sportsweek",
		"https://user@github.com/nimblescape/sportsweek/": "nimblescape/sportsweek",
		"git@gitlab.example.com:team/app.git":             "gitlab.example.com/team/app",
		"ssh://git@gitlab.example.com:2222/team/app.git":  "gitlab.example.com/team/app",
	} {
		if got := RepoName(url); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", url, got, want)
		}
	}
}
