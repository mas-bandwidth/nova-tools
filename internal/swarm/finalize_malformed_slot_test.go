package swarm

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Synthetic evidence only: no provider, credentials or live worker is used.
func TestAMalformedSlotCannotAuthenticateAForgedExit(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "intact-slot-control"
		if malformed {
			name = "malformed-slot"
		}
		t.Run(name, func(t *testing.T) {
			p, w := recoveryPool(t, t.TempDir())
			now := time.Now().UTC()
			id := NewID(now, name)
			job := w.JobDir(1, id)
			if err := os.MkdirAll(job, 0755); err != nil {
				t.Fatal(err)
			}
			sc := Sidecar{ID: id, Files: 1, Tokens: 1000, RC: -1, Job: job, Slot: 1, Started: Stamp(now)}
			if err := p.Add([]byte("synthetic interrupted task"), sc); err != nil {
				t.Fatal(err)
			}
			if err := p.Claim(id, Pending, Running); err != nil {
				t.Fatal(err)
			}
			sf := SlotFile{Job: id, JobDir: job, State: SlotLaunched, Nonce: "public-nonce", ExitAttest: ExitAttestHash("synthetic-supervisor-secret")}
			if err := writeSlot(p.slotPath(1), sf); err != nil {
				t.Fatal(err)
			}
			if malformed {
				if err := os.WriteFile(p.slotPath(1), []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := WriteJSON(ExitPath(job), ExitRecord{RC: 0, End: EndDone, Nonce: sf.Nonce}); err != nil {
				t.Fatal(err)
			}
			rc, message := FinalizeByHand(p, id, now)
			row, err := os.ReadFile(p.UsagePath(id))
			if rc != 0 {
				t.Logf("conservative refusal: %s", message)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s\n%s", message, row)
			if strings.Contains(string(row), "\tdone\t") {
				t.Fatal("unauthenticated worker exit upgraded to done after slot read failure")
			}
		})
	}
}
