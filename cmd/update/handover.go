package update

import (
	"github.com/nimblescape/nimble/pkg/config"
	"github.com/nimblescape/nimble/pkg/keys"
	"github.com/nimblescape/nimble/pkg/nimble"
	"github.com/nimblescape/nimble/pkg/setup"
)

// KeysSynced notes that a host's keys were synced (it was hardened with the current
// keys/): it trusts every renewed key now.
func KeysSynced(host string) {
	if len(keys.Handovers()) == 0 {
		return
	}
	if err := keys.MarkUpdated(host); err != nil {
		nimble.Warn("key handover: " + err.Error())
	}
}

// FinishHandovers looks at the key renewals in progress after hosts were updated. A daily
// key's handover that every managed host of its domain has is ended: the old key goes
// from this Mac. A YubiKey's old key can only go with the YubiKey plugged in, so nimble
// says so. A handover past its deadline with hosts still waiting is a warning.
func FinishHandovers() {
	hs := keys.Handovers()
	if len(hs) == 0 {
		return
	}
	c, err := config.Load()
	if err != nil {
		return
	}
	for _, h := range hs {
		waiting := h.Waiting(c)
		switch {
		case len(waiting) == 0 && h.Kind() == "enclave":
			handle := keys.HandlePath(h.Label())
			if nimble.Exists(handle + ".old.pub") {
				nimble.Step("every host has the renewed key " + h.Name() + ": removing the old one from this Mac")
				if err := keys.DropOldEnclave(handle); err != nil {
					nimble.Warn("the old key stays: " + err.Error() + " — nimble key enclave --drop-old")
					continue
				}
				if err := setup.SSHConfig(c); err != nil {
					nimble.Warn("ssh config: " + err.Error())
				}
			}
			if err := keys.EndHandover(h.Key); err != nil {
				nimble.Warn("key handover: " + err.Error())
			}
			nimble.Say("handover of " + h.Key + " done")
		case len(waiting) == 0:
			nimble.Say(h.Key + ": " + h.Describe(c))
		case h.Overdue():
			nimble.Warn(h.Key + ": " + h.Describe(c))
		}
	}
}
