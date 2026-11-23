package keys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/nimble"
	"gopkg.in/yaml.v3"
)

// A key renewal hands over from the old key to the new one. The new key is the primary
// one at once: it is in keys/, so every host trusts it from its next nimble update (and
// stops trusting the old one then). The old key stays usable on this workstation — the
// Mac's old Secure Enclave key, or the old credential on the YubiKey — so the hosts that
// weren't updated yet are still reachable. The handover ends when every managed host that
// trusts the key has been updated (nimble update and init mark them), or at its deadline.
// Its state is this workstation's: cache/handover.yaml, never committed.

// DefaultHandover is how long a renewal keeps the old key at most.
const DefaultHandover = 14 * 24 * time.Hour

// Handover is one renewal in progress.
type Handover struct {
	Key    string    `yaml:"key"`              // the renewed key in keys/: enclave/<mac>-<label> or yubikey/<name>
	Old    string    `yaml:"old"`              // the old key's fingerprint
	OldApp string    `yaml:"oldApp,omitempty"` // yubikey: the FIDO application of the old credential
	Since  time.Time `yaml:"since"`
	Until  time.Time `yaml:"until"`          // the deadline
	Done   []string  `yaml:"done,omitempty"` // hosts updated since the renewal
}

// Kind is enclave or yubikey; Name the key's name in keys/<kind>/.
func (h Handover) Kind() string { k, _, _ := strings.Cut(h.Key, "/"); return k }
func (h Handover) Name() string { _, n, _ := strings.Cut(h.Key, "/"); return n }

// Label is the domain label of a renewed daily key (enclave/<mac>-<label>).
func (h Handover) Label() string {
	if i := strings.LastIndex(h.Name(), "-"); h.Kind() == "enclave" && i > 0 {
		return h.Name()[i+1:]
	}
	return ""
}

// Waiting lists the managed hosts that trust the key and haven't been updated since the
// renewal: every managed host for a YubiKey backup, those of the label's domains for a
// daily key. Hosts reached over ssh only aren't nimble's to update: their keys are
// managed by hand.
func (h Handover) Waiting(c *config.Config) []string {
	hs, _ := config.Hosts()
	done := map[string]bool{}
	for _, d := range h.Done {
		done[d] = true
	}
	var out []string
	for _, x := range hs {
		if !x.Managed() || done[x.Name] {
			continue
		}
		if h.Kind() == "enclave" {
			if d, err := c.Domain(x.Domain); err != nil || d.Label != h.Label() {
				continue
			}
		}
		out = append(out, x.Name)
	}
	return out
}

// Overdue reports whether the deadline has passed.
func (h Handover) Overdue() bool { return time.Now().After(h.Until) }

// Describe is a line about the handover's state.
func (h Handover) Describe(c *config.Config) string {
	waiting := h.Waiting(c)
	switch {
	case len(waiting) == 0 && h.Kind() == "yubikey":
		return "every host has the new key — the old credential on the YubiKey can go: nimble key yubikey " + h.Name() + " --drop-old"
	case len(waiting) == 0:
		return "every host has the new key"
	case h.Overdue():
		return "deadline " + h.Until.Format("2006-01-02") + " passed — still without the new key: " + strings.Join(waiting, ", ") + " (nimble update " + strings.Join(waiting, " ") + ")"
	}
	return fmt.Sprintf("%d host(s) still without the new key: %s — the old key is kept until they have it, at most until %s (nimble update all)",
		len(waiting), strings.Join(waiting, ", "), h.Until.Format("2006-01-02"))
}

func handoverFile() string { return nimble.CacheDir("handover.yaml") }

// Handovers lists the renewals in progress on this workstation.
func Handovers() []Handover {
	var hs []Handover
	if b, err := os.ReadFile(handoverFile()); err == nil {
		_ = yaml.Unmarshal(b, &hs)
	}
	return hs
}

// FindHandover returns the handover of a key (kind/name), if one is in progress.
func FindHandover(key string) (Handover, bool) {
	for _, h := range Handovers() {
		if h.Key == key {
			return h, true
		}
	}
	return Handover{}, false
}

func saveHandovers(hs []Handover) error {
	if len(hs) == 0 {
		if err := os.Remove(handoverFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := yaml.Marshal(hs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(handoverFile()), 0o755); err != nil {
		return err
	}
	head := "# Key renewals in progress on this workstation (nimble key enclave|yubikey --renew).\n"
	return os.WriteFile(handoverFile(), append([]byte(head), b...), 0o644)
}

// StartHandover records a renewal (replacing an earlier one of the same key).
func StartHandover(h Handover) error {
	hs := []Handover{h}
	for _, x := range Handovers() {
		if x.Key != h.Key {
			hs = append(hs, x)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Key < hs[j].Key })
	return saveHandovers(hs)
}

// EndHandover forgets the renewal of a key.
func EndHandover(key string) error {
	var hs []Handover
	for _, x := range Handovers() {
		if x.Key != key {
			hs = append(hs, x)
		}
	}
	return saveHandovers(hs)
}

// MarkUpdated notes that a host was updated: its keys are synced, so it trusts the renewed
// keys now.
func MarkUpdated(host string) error {
	hs := Handovers()
	if len(hs) == 0 {
		return nil
	}
	for i := range hs {
		found := false
		for _, d := range hs[i].Done {
			found = found || d == host
		}
		if !found {
			hs[i].Done = append(hs[i].Done, host)
		}
	}
	return saveHandovers(hs)
}

var durationRe = regexp.MustCompile(`^(\d+)([dw])$`)

// ParseHandover reads a handover time: 14d, 2w, or a Go duration such as 36h.
func ParseHandover(s string) (time.Duration, error) {
	if m := durationRe.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		n, _ := strconv.Atoi(m[1])
		days := map[string]int{"d": 1, "w": 7}[m[2]]
		return time.Duration(n*days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("handover time %q: e.g. 14d, 2w or 36h", s)
	}
	return d, nil
}
