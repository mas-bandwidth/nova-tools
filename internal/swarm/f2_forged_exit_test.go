package swarm

import (
	"encoding/json"
	"strings"
	"testing"
)

// AND THE NONCE IS OUT OF THE WORKER'S WRITE SET. `<job>/pid` is the identity record beside
// the job, inside the first `--write`; the nonce was written into it twice (supervise.go)
// and read back by nothing. The record keeps the pids and the start stamps, which are what
// its readers want (`readJobProc`, `FinalizeByHand`), and carries no launch nonce.
func TestTheIdentityRecordBesideTheJobCarriesNoLaunchNonce(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(PidRecord{Job: "job", Slot: 1, State: SlotLaunched, Pid: 2, Pgid: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "nonce") {
		t.Errorf("<job>/pid is inside the worker's write set and carries the launch nonce: %s", raw)
	}
}
