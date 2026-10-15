package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// hetzner1NativeLog is the native log of a metered work launch native refused before the
// harness started, as hetzner1 wrote it on 2026-10-15 for tr15-up-r4.w2 on the
// pro-mercury3-pro route (the slot's native.log, its pool identity.tsv gone): staged and
// framed, then refused, with no rc= line and no harness output, so no harness ran.
const hetzner1NativeLog = `STAGE OK bench=hetzner1 repo=https://github.com/mas-bandwidth/nova-tools.git base=34ea5c0c secs=6 clone=3.7 fetch=1.4 checkout=1.2
STAGE CARRY staged=34ea5c0cb662 tip=34ea5c0cb662 of v1.3/comms carry=none
FRAME OK secs=0.1
NATIVE REFUSED: pool /home/nova/rowan-working/tmp has no identity row in identity.tsv: open /home/nova/rowan-working/tmp/identity.tsv: no such file or directory; refusing to launch under nobody's name; remedy: make -C fleet converge (the repository) writes the pool identity.tsv; or give the loop --identity <owner>,<name>,<email> in its nova-config argv
`

// refusedLaunch writes a slot as hetzner1's: the job staged (JOB.md), no usage.tsv, no
// RESULT.md, and the native log above; ran says whether the harness output log exists.
func refusedLaunch(t *testing.T, billing string, ran bool) *nativeChild {
	t.Helper()
	dir := t.TempDir()
	job := filepath.Join(dir, "jobs", "tr15-up-r4.w2")
	require.NoError(t, os.MkdirAll(job, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(job, "JOB.md"), []byte("# JOB\n"), 0o644))
	if ran {
		require.NoError(t, os.WriteFile(filepath.Join(job, "harness-output.log"), []byte("the harness ran\n"), 0o644))
	}
	logPath := filepath.Join(dir, "tr15-up-r4.w2.g1.e21.native.log")
	require.NoError(t, os.WriteFile(logPath, []byte(hetzner1NativeLog), 0o644))
	return &nativeChild{billing: billing, card: "tr15-up-r4.w2", job: job, logPath: logPath, results: filepath.Join(dir, "results")}
}

// A metered launch native refused before the harness started spent nothing: its finish
// records the usage unknown, typed, with no raw_usage the paired receipt contract refuses,
// so the failed finish lands and the sprint judges it. Red at BASE: the usage carried
// raw_usage=INVALID, `nova-sprint finish --receipt-contract 2` refused it "invalid raw usage
// JSON", and the member ran the card again on the next pass (hetzner1, 2026-10-15).
func TestALaunchRefusedBeforeTheHarnessRecordsUnknownUsageTheContractTakes(t *testing.T) {
	t.Parallel()
	for _, billing := range cardcost.Billings {
		t.Run(billing, func(t *testing.T) {
			t.Parallel()
			r := refusedLaunch(t, billing, false).Result()
			assert.False(t, r.Ran)
			assert.Contains(t, r.Report, "native refused: pool /home/nova/rowan-working/tmp has no identity row")
			assert.NotContains(t, r.Usage, "raw_usage=")
			assert.Contains(t, r.Usage, "usage_source_error=no-launch")
			u, err := cardcost.ParseUsageChecked(r.Usage)
			require.NoError(t, err, "the receipt contract's parser takes the finish's usage: %s", r.Usage)
			assert.Equal(t, cardcost.ReceiptVersion, u.ReceiptVersion, "still a typed receipt: never legacy")
			assert.Equal(t, billing, u.Billing)
			assert.Equal(t, cardcost.None(), u.Tokens, "unknown, never a measured zero")
			assert.Nil(t, u.Raw)
		})
	}
}

// The fence stays for a launch whose harness ran: its missing receipt is a lost one, and
// its usage is still refused rather than recorded (TestTypedLaunchMissingAllReceiptsCannotBecomeLegacy).
func TestALaunchWhoseHarnessRanStillRefusesAMissingReceipt(t *testing.T) {
	t.Parallel()
	r := refusedLaunch(t, cardcost.BillingMetered, true).Result()
	assert.Contains(t, r.Usage, "raw_usage=INVALID")
	assert.Error(t, cardcost.ValidateUsage(r.Usage))
}
