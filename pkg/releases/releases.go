// Package releases looks up the published versions of the pinned tools.
package releases

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var client = &http.Client{Timeout: 30 * time.Second}

func getJSON(url, accept string, into any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "nimble-version-check")
	req.Header.Set("Accept", accept)
	if t := os.Getenv("GITHUB_TOKEN"); t != "" && strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

type nodeRelease struct {
	Version string
	LTS     any // false, or the codename of the LTS line the release belongs to
}

var (
	nodeIndex   []nodeRelease // fetched once: Node and NodeLTS both read it
	nodeIndexMu sync.Mutex
)

func nodeReleases() ([]nodeRelease, error) {
	nodeIndexMu.Lock()
	defer nodeIndexMu.Unlock()
	if nodeIndex == nil {
		var rs []nodeRelease
		if err := getJSON("https://nodejs.org/dist/index.json", "application/json", &rs); err != nil {
			return nil, err
		}
		nodeIndex = rs
	}
	return nodeIndex, nil
}

// Node lists all Node.js releases.
func Node() ([]string, error) {
	rs, err := nodeReleases()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Version
	}
	return out, nil
}

// NodeLTS marks the Node.js releases of an LTS line (x.y.z) with "LTS <codename>".
func NodeLTS() (map[string]string, error) {
	rs, err := nodeReleases()
	if err != nil {
		return nil, err
	}
	return nodeLTS(rs), nil
}

func nodeLTS(rs []nodeRelease) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		if name, ok := r.LTS.(string); ok && name != "" {
			out[strings.TrimPrefix(r.Version, "v")] = "LTS " + name
		}
	}
	return out
}

// Go lists the stable Go releases.
func Go() ([]string, error) {
	var rs []struct {
		Version string
		Stable  bool
	}
	if err := getJSON("https://go.dev/dl/?mode=json&include=all", "application/json", &rs); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rs {
		if r.Stable {
			out = append(out, strings.TrimPrefix(r.Version, "go"))
		}
	}
	return out, nil
}

// GitHubReleases lists a repo's published, non-prerelease release tags.
func GitHubReleases(repo string) ([]string, error) {
	var rs []struct {
		Tag        string `json:"tag_name"`
		Draft      bool
		Prerelease bool
	}
	if err := getJSON("https://api.github.com/repos/"+repo+"/releases?per_page=100", "application/vnd.github+json", &rs); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rs {
		if !r.Draft && !r.Prerelease {
			out = append(out, r.Tag)
		}
	}
	return out, nil
}

// NPM lists the published versions of an npm package.
func NPM(name string) ([]string, error) {
	var d struct{ Versions map[string]json.RawMessage }
	url := "https://registry.npmjs.org/" + strings.ReplaceAll(name, "/", "%2F")
	if err := getJSON(url, "application/vnd.npm.install-v1+json", &d); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(d.Versions))
	for v := range d.Versions {
		out = append(out, v)
	}
	return out, nil
}

// Python lists the CPython builds in the newest python-build-standalone release — what uv
// downloads. (The pinned uv's own list ends at its release date, so it misses newer patches.)
func Python() ([]string, error) {
	var rel struct {
		Assets []struct{ Name string } `json:"assets"`
	}
	if err := getJSON("https://api.github.com/repos/astral-sh/python-build-standalone/releases/latest", "application/vnd.github+json", &rel); err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^cpython-(\d+\.\d+\.\d+)\+\d+-`)
	seen := map[string]bool{}
	var out []string
	for _, a := range rel.Assets {
		if m := re.FindStringSubmatch(a.Name); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no CPython builds in the newest python-build-standalone release")
	}
	return out, nil
}

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func parse(s string) (v [3]int, ok bool) {
	m := semver.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return v, false
	}
	for i := range v {
		fmt.Sscan(m[i+1], &v[i])
	}
	return v, true
}

// NewestCompatible returns the newest release current can move to without breaking
// compatibility — level is the tool's scheme for that: "patch" (same x.y, newer z) or
// "minor" (same x, newer y.z) or "major" (any newer release, for tools whose every release
// stays compatible, like gcloud) — or "" if there is none. Pre-releases never match.
func NewestCompatible(current string, releases []string, level string) string {
	cur, ok := parse(current)
	if !ok {
		return ""
	}
	best, found := cur, false
	for _, r := range releases {
		v, ok := parse(r)
		if !ok || (level != "major" && (v[0] != cur[0] || (level != "minor" && v[1] != cur[1]))) {
			continue
		}
		if less(best, v) {
			best, found = v, true
		}
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", best[0], best[1], best[2])
}

// NewestFeature returns the newest release beyond what NewestCompatible may take (a newer
// minor or major for "patch", a newer major for "minor"), or "" if there is none.
func NewestFeature(current string, releases []string, level string) string {
	cur, ok := parse(current)
	if !ok {
		return ""
	}
	best, found := cur, false
	for _, r := range releases {
		v, ok := parse(r)
		if !ok || level == "major" || (v[0] == cur[0] && (level == "minor" || v[1] == cur[1])) {
			continue // within the compatible range
		}
		if less(best, v) {
			best, found = v, true
		}
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", best[0], best[1], best[2])
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Sorted returns the stable releases newest first (pre-releases and odd tags dropped).
func Sorted(versions []string) []string {
	var vs [][3]int
	seen := map[[3]int]bool{}
	for _, s := range versions {
		if v, ok := parse(s); ok && !seen[v] && strings.Count(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".") == 2 && !strings.ContainsAny(s, "-+abr") {
			seen[v] = true
			vs = append(vs, v)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return less(vs[j], vs[i]) })
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	}
	return out
}
