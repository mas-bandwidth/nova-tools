package swarm

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestSwarmConfigRecordsDualRead is the reader half of the swarm config and
// slot records' table adoption (v1.5 aspect 13; the owner's rules S2 and S4):
// the exit record (pkg/swarm/slot.go) is read alike from its fixed binary
// image, the padded standard base64 of that image, and the legacy form its
// writer writes today; a damaged value of each form is refused by name
// (`EXIT_RECORD REFUSED: <reason>; run: nova-swarm -h`) and never read as
// absent, empty or zero; and reads are counted per record and per form. The
// worker description, the HarnessConfigRecord and the FenceConfig record
// (pkg/swarm/worker.go, pkg/swarm/fence.go) are read the same way, and a value
// past a declared bound of theirs is refused as clamped; every strict prefix
// of every form is refused by name and no flipped byte reads as a zero record.
//
// Unit tier: no clock, no socket, no program, no store. Files are read from
// the t.TempDir() the test writes.
func TestSwarmConfigRecordsDualRead(t *testing.T) {
	t.Parallel()

	r := exitReader()
	want := exitDoc{Rc: 1, Signal: "SIGTERM", Ended: "2026-10-17T00:00:00Z", Survivors: 2,
		Nonce: "abcdef123456", Attest: "attest", End: "end", Spent: 3, Observed: true, Reason: "done"}

	wire := writeExit(t, r, &want, schemaread.FormBinary)
	b64 := writeExit(t, r, &want, schemaread.FormBase64)
	js := writeExit(t, r, &want, schemaread.FormJSON)
	legacy, err := json.Marshal(ExitRecord{RC: 1, Signal: "SIGTERM", Ended: "2026-10-17T00:00:00Z", Survivors: 2,
		Nonce: "abcdef123456", Attest: "attest", End: "end", Spent: 3, Observed: true, Reason: "done"})
	require.NoError(t, err)

	dir := t.TempDir()
	forms := map[string]struct {
		body []byte
		form schemaread.Form
	}{
		// The schema JSON text and the legacy JSON both open with `{`, and a
		// reader recognises the legacy form first (schemaread Detect), so the
		// JSON text reads as legacy; the binary and base64 forms are distinct.
		"binary": {wire, schemaread.FormBinary},
		"base64": {b64, schemaread.FormBase64},
		"json":   {js, schemaread.FormLegacy},
		"legacy": {legacy, schemaread.FormLegacy},
	}
	for name, f := range forms {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, f.body, 0o600))
		got, form, err := ReadExitFile(path)
		require.NoError(t, err, name)
		assert.Equal(t, f.form, form, name)
		assert.Equal(t, want, exitDoc{Rc: int64(got.RC), Signal: got.Signal, Ended: got.Ended, Survivors: int64(got.Survivors),
			Nonce: got.Nonce, Attest: got.Attest, End: got.End, Spent: int64(got.Spent), Observed: got.Observed, Partial: got.Partial, Reason: got.Reason}, name)
		raw, err := os.ReadFile(path)
		require.NoError(t, err, name)
		got2, res, err := r.Read(raw)
		require.NoError(t, err, name)
		assert.Equal(t, f.form, res.Form, name)
		assert.Equal(t, want, got2, name)
	}

	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormLegacy))

	damaged := map[string][]byte{
		"truncated-wire": wire[:len(wire)-1],
		"truncated-b64":  b64[:len(b64)-2],
		"truncated-json": js[:len(js)/2],
		"bad-json":       []byte(`{"rc":`),
		"bad-legacy":     []byte(`{"rc": "not-an-int"}`),
		"empty":          {},
	}
	for name, body := range damaged {
		_, _, err := r.Read(body)
		require.Error(t, err, name)
		ref := exitRefusal(t, err)
		assert.Equal(t, "EXIT_RECORD", ref.Name, name)
		assert.NotEqual(t, "", ref.Reason, name)
	}
	assert.Equal(t, uint64(len(damaged)), r.Counts().Refused("malformed")+r.Counts().Refused("unknown_form")+r.Counts().Refused("absent_required"))

	// The declared model record (pkg/swarm/declaremodel.go) is read alike from
	// its fixed binary image, the padded standard base64 of that image, and the
	// legacy JSON its writer carries today; damage is refused by name and reads
	// are counted per form.
	dm := declaredModelReader()
	dwant := declaredModelDoc{Provider: "deepseek", Model: "deepseek-chat"}
	dwire := writeDeclaredModel(t, dm, &dwant, schemaread.FormBinary)
	db64 := writeDeclaredModel(t, dm, &dwant, schemaread.FormBase64)
	djs := writeDeclaredModel(t, dm, &dwant, schemaread.FormJSON)
	dlegacy := []byte(`{"provider": "deepseek", "model": "deepseek-chat"}`)
	for name, f := range map[string]struct {
		body []byte
		form schemaread.Form
	}{
		"binary": {dwire, schemaread.FormBinary},
		"base64": {db64, schemaread.FormBase64},
		"json":   {djs, schemaread.FormLegacy},
		"legacy": {dlegacy, schemaread.FormLegacy},
	} {
		got, res, err := dm.Read(f.body)
		require.NoError(t, err, name)
		assert.Equal(t, f.form, res.Form, name)
		assert.Equal(t, dwant, got, name)
	}
	assert.Equal(t, uint64(1), dm.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), dm.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), dm.Counts().Reads(schemaread.FormLegacy))
	for name, damaged := range map[string][]byte{
		"truncated-wire": dwire[:len(dwire)-1],
		"truncated-b64":  db64[:len(db64)-2],
		"truncated-json": djs[:len(djs)/2],
		"bad-json":       []byte(`{"provider":`),
		"bad-legacy":     []byte(`{"provider": 7}`),
		"empty":          {},
	} {
		_, _, err := dm.Read(damaged)
		require.Error(t, err, name)
		ref := exitRefusal(t, err)
		assert.Equal(t, "DECLARED_MODEL", ref.Name, name)
		assert.NotEqual(t, "", ref.Reason, name)
	}
	assert.Equal(t, uint64(6), dm.Counts().Refused("malformed")+dm.Counts().Refused("unknown_form")+dm.Counts().Refused("absent_required"))

	// Every damaged image of the exit and declared model records is refused by
	// name or read whole, never as a zero record.
	mutateRecord(t, "EXIT_RECORD", exitReader(), map[string][]byte{"binary": wire, "base64": b64, "json": js, "legacy": legacy})
	mutateRecord(t, "DECLARED_MODEL", declaredModelReader(), map[string][]byte{"binary": dwire, "base64": db64, "json": djs, "legacy": dlegacy})

	t.Run("worker", testWorkerRecordDualRead)
	t.Run("harness-config", testHarnessConfigRecordDualRead)
	t.Run("fence-config", testFenceConfigDualRead)
	t.Run("read-sites", testSwarmReadSitesReadEveryForm)
}

// dualRead writes want in each table form, reads each back and the legacy
// fixture equal under its form, counts one read per form, and returns the
// bodies by form name for the damage passes.
func dualRead[T any](t *testing.T, r *schemaread.Reader[T], want T, legacy []byte, jsonForm schemaread.Form) map[string][]byte {
	t.Helper()
	bodies := map[string][]byte{"legacy": legacy}
	for name, form := range map[string]schemaread.Form{"binary": schemaread.FormBinary, "base64": schemaread.FormBase64, "json": schemaread.FormJSON} {
		out, err := r.Write(&want, form)
		require.NoError(t, err, name)
		require.NotEmpty(t, out, name)
		bodies[name] = out
	}
	wantForm := map[string]schemaread.Form{"binary": schemaread.FormBinary, "base64": schemaread.FormBase64, "json": jsonForm, "legacy": schemaread.FormLegacy}
	for name, body := range bodies {
		got, res, err := r.Read(body)
		require.NoError(t, err, name)
		assert.Equal(t, wantForm[name], res.Form, name)
		assert.Equal(t, want, got, name)
	}
	legacyReads := uint64(1)
	if jsonForm == schemaread.FormLegacy {
		legacyReads = 2
	} else {
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON), "one schema JSON read")
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary), "one binary read")
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64), "one base64 read")
	assert.Equal(t, legacyReads, r.Counts().Reads(schemaread.FormLegacy), "legacy reads")
	return bodies
}

// mutateRecord is the damage pass over every form of one record: each strict
// prefix of a body is refused by name, and each single flipped byte is either
// refused by name or read as a whole record, never as a zero one. The flips
// are every position of every body, so the pass is deterministic.
func mutateRecord[T any](t *testing.T, name string, r *schemaread.Reader[T], bodies map[string][]byte) {
	t.Helper()
	var zero T
	refused := 0
	for form, body := range bodies {
		body = bytes.TrimSpace(body)
		for n := 0; n < len(body); n++ {
			_, _, err := r.Read(body[:n])
			require.Error(t, err, "%s %s truncated to %d bytes reads", name, form, n)
			assert.Equal(t, name, exitRefusal(t, err).Name, "%s %s truncated to %d", name, form, n)
			refused++
		}
		for i := range body {
			flipped := bytes.Clone(body)
			flipped[i] ^= 0xff
			got, _, err := r.Read(flipped)
			if err != nil {
				assert.Equal(t, name, exitRefusal(t, err).Name, "%s %s flipped at %d", name, form, i)
				refused++
				continue
			}
			assert.NotEqual(t, zero, got, "%s %s flipped at %d reads as a zero record", name, form, i)
		}
	}
	assert.Positive(t, refused, name)
	var total uint64
	for _, reason := range []string{schemaread.ReasonMalformed, schemaread.ReasonClamped, schemaread.ReasonKindMismatch,
		schemaread.ReasonUnknownForm, schemaread.ReasonWireDamaged, schemaread.ReasonAbsentRequired} {
		total += r.Counts().Refused(reason)
	}
	assert.GreaterOrEqual(t, total, uint64(refused), "%s: every refusal is counted under a named reason", name)
}

// refusedAs asserts body is refused by name with reason.
func refusedAs[T any](t *testing.T, r *schemaread.Reader[T], name, reason string, body []byte) {
	t.Helper()
	_, _, err := r.Read(body)
	require.Error(t, err, string(body))
	ref := exitRefusal(t, err)
	assert.Equal(t, name, ref.Name, string(body))
	assert.Equal(t, reason, ref.Reason, string(body))
	assert.Contains(t, err.Error(), name+" REFUSED: "+reason, string(body))
}

func testWorker(dir string) Worker {
	return Worker{Name: "w1", Provider: "deepseek", Model: "deepseek-chat", BaseURL: "https://api.example.invalid/v1",
		EnvVar: "DEEPSEEK_API_KEY", KeyFile: filepath.Join(dir, "keys", "deepseek"), Usage: UsageOpenCode, Class: WorkerClassPublic,
		Harness: "opencode", HarnessArgs: []string{"run", "--model", ModelPlaceholder, "--", PromptPlaceholder},
		WorkerDir: filepath.Join(dir, "worker"), Deadline: "20m", Board: "board", MaxTurns: 40, MaxCacheRead: 1000000,
		InputLimitPhrases: []string{"input token limit exceeded"}, LaunchGrace: "15s", KeepData: true}
}

// The worker description (pkg/swarm/worker.go LoadWorker) reads alike in
// every form; the schema JSON text is told from the legacy description by its
// worker_class key and numeric usage, which the legacy decoder refuses.
func testWorkerRecordDualRead(t *testing.T) {
	t.Parallel()
	r := workerReader()
	want := testWorker("/srv/jobs")
	legacy, err := json.Marshal(want)
	require.NoError(t, err)
	bodies := dualRead(t, r, want, legacy, schemaread.FormJSON)
	mutateRecord(t, "WORKER_RECORD", workerReader(), bodies)

	// The legacy decoder stays strict: an unknown field is a refusal.
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonMalformed, []byte(`{"name":"x","key_fil":"/k"}`))
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonMalformed, []byte(`{"name":"x","usage":"opencode","max_turns":"many"}`))
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonUnknownForm, []byte{})
	// A value past a declared bound is refused as clamped, never clamped.
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonClamped, []byte(`{"name":"x","usage":3,"worker_class":1}`))
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonClamped, []byte(`{"name":"x","usage":1,"worker_class":7}`))
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonClamped, []byte(`{"name":"x","usage":1,"max_turns":-1}`))
	// A NUL in a path is refused, never carried to the wall.
	refusedAs(t, r, "WORKER_RECORD", schemaread.ReasonMalformed, []byte(`{"name":"x","usage":"opencode","worker_dir":"/a\u0000b"}`))
	assert.Equal(t, uint64(7), r.Counts().Refused(schemaread.ReasonMalformed)+r.Counts().Refused(schemaread.ReasonClamped)+r.Counts().Refused(schemaread.ReasonUnknownForm))
}

// The HarnessConfigRecord (pkg/swarm/worker.go HarnessConfig) reads alike in
// every form; an absent provider, model or env_var is refused by name.
func testHarnessConfigRecordDualRead(t *testing.T) {
	t.Parallel()
	r := harnessConfigReader()
	want := harnessConfigDoc{Provider: "deepseek", Model: "deepseek-chat", EnvVar: "DEEPSEEK_API_KEY", BaseURL: "https://api.example.invalid/v1"}
	legacy := []byte(`{"provider": "deepseek", "model": "deepseek-chat", "env_var": "DEEPSEEK_API_KEY", "base_url": "https://api.example.invalid/v1"}`)
	bodies := dualRead(t, r, want, legacy, schemaread.FormLegacy)
	mutateRecord(t, "HARNESS_CONFIG_RECORD", harnessConfigReader(), bodies)

	refusedAs(t, r, "HARNESS_CONFIG_RECORD", schemaread.ReasonAbsentRequired, []byte(`{"provider": "deepseek", "model": "deepseek-chat"}`))
	refusedAs(t, r, "HARNESS_CONFIG_RECORD", schemaread.ReasonMalformed, []byte(`{"provider": 7}`))
	_, err := r.Write(&harnessConfigDoc{Provider: "deepseek"}, schemaread.FormBinary)
	require.Error(t, err, "the writer refuses what the reader refuses")
	_, form, err := ReadHarnessConfigRecord(bodies["base64"])
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBase64, form)
}

// The FenceConfig record (pkg/swarm/fence.go FencePermission) reads alike in
// every form; an action or webfetch past its range is refused as clamped.
func testFenceConfigDualRead(t *testing.T) {
	t.Parallel()
	r := fenceConfigReader()
	want := fenceConfigOf("/srv/jobs/j1", []string{"/sys/kernel/security/lsm"})
	require.Len(t, want.Rules, 7, "one deny-all, two job patterns and four for the read")
	assert.Equal(t, fenceRuleDoc{"*", 2}, want.Rules[0])
	assert.Equal(t, uint8(2), want.Webfetch)
	legacy, err := json.Marshal(want)
	require.NoError(t, err)
	bodies := dualRead(t, r, want, legacy, schemaread.FormLegacy)
	mutateRecord(t, "FENCE_CONFIG", fenceConfigReader(), bodies)

	refusedAs(t, r, "FENCE_CONFIG", schemaread.ReasonClamped, []byte(`{"job_dir": "/j", "webfetch": 3}`))
	refusedAs(t, r, "FENCE_CONFIG", schemaread.ReasonClamped, []byte(`{"job_dir": "/j", "rules": [{"pattern": "/j/*", "action": 9}], "webfetch": 2}`))
	refusedAs(t, r, "FENCE_CONFIG", schemaread.ReasonAbsentRequired, []byte(`{"job_dir": "/j", "rules": [{"pattern": "/j/*", "action": 0}], "webfetch": 2}`))
	refusedAs(t, r, "FENCE_CONFIG", schemaread.ReasonAbsentRequired, []byte(`{"webfetch": 2}`))
	refusedAs(t, r, "FENCE_CONFIG", schemaread.ReasonMalformed, []byte(`{"job_dir": "/j", "reads": [7]}`))
	_, err = r.Write(&fenceConfigDoc{JobDir: "/j", Webfetch: 3}, schemaread.FormBinary)
	require.Error(t, err, "the writer refuses a value past its bound before a byte is written")
	_, form, err := ReadFenceConfig(bodies["binary"])
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBinary, form)
}

// The wired read sites read every form from a file the test writes: the exit
// record through ReadJSON (pkg/swarm/slot.go), the worker description through
// LoadWorker (pkg/swarm/worker.go), each counted in the package's counters.
func testSwarmReadSitesReadEveryForm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	counts := SwarmReadCounts()
	for _, name := range []string{"EXIT_RECORD", "DECLARED_MODEL", "WORKER_RECORD", "HARNESS_CONFIG_RECORD", "FENCE_CONFIG"} {
		require.NotNil(t, counts[name], name)
	}

	exit := ExitRecord{RC: 2, Signal: "SIGKILL", Ended: "2026-10-17T00:00:00Z", Survivors: 1, Nonce: "0123456789ab", Spent: 7, Reason: "deadline"}
	doc := exitDoc{Rc: 2, Signal: "SIGKILL", Ended: "2026-10-17T00:00:00Z", Survivors: 1, Nonce: "0123456789ab", Spent: 7, Reason: "deadline"}
	legacy, err := json.Marshal(exit)
	require.NoError(t, err)
	for name, form := range map[string]schemaread.Form{"binary": schemaread.FormBinary, "base64": schemaread.FormBase64, "json": schemaread.FormJSON, "legacy": schemaread.FormLegacy} {
		body := legacy
		if form != schemaread.FormLegacy {
			body, err = exitReader().Write(&doc, form)
			require.NoError(t, err, name)
		}
		path := filepath.Join(dir, "exit-"+name+".json")
		require.NoError(t, os.WriteFile(path, body, 0o600))
		before := counts["EXIT_RECORD"].Reads(schemaread.FormBinary) + counts["EXIT_RECORD"].Reads(schemaread.FormBase64) + counts["EXIT_RECORD"].Reads(schemaread.FormLegacy)
		var got ExitRecord
		require.NoError(t, ReadJSON(path, &got), name)
		assert.Equal(t, exit, got, name)
		after := counts["EXIT_RECORD"].Reads(schemaread.FormBinary) + counts["EXIT_RECORD"].Reads(schemaread.FormBase64) + counts["EXIT_RECORD"].Reads(schemaread.FormLegacy)
		assert.Greater(t, after, before, "%s: the read is counted", name)
	}
	damaged := filepath.Join(dir, "exit-damaged.json")
	require.NoError(t, os.WriteFile(damaged, []byte(`{"rc": "two"}`), 0o600))
	var none ExitRecord
	err = ReadJSON(damaged, &none)
	require.Error(t, err, "a damaged exit record is never read as a zero record")
	assert.Equal(t, "EXIT_RECORD", exitRefusal(t, err).Name)

	want := testWorker(dir)
	legacyWorker, err := json.Marshal(want)
	require.NoError(t, err)
	for name, form := range map[string]schemaread.Form{"binary": schemaread.FormBinary, "base64": schemaread.FormBase64, "json": schemaread.FormJSON, "legacy": schemaread.FormLegacy} {
		body := legacyWorker
		if form != schemaread.FormLegacy {
			body, err = workerReader().Write(&want, form)
			require.NoError(t, err, name)
		}
		path := filepath.Join(dir, "worker-"+name+".json")
		require.NoError(t, os.WriteFile(path, body, 0o600))
		before := counts["WORKER_RECORD"].Reads(form)
		got, problems := LoadWorker(path)
		require.Empty(t, problems, name)
		assert.Equal(t, want, got, name)
		assert.Greater(t, counts["WORKER_RECORD"].Reads(form), before, "%s: the read is counted under its form", name)
	}
	bad := filepath.Join(dir, "worker-bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(`{"name":"x","key_fil":"/k"}`), 0o600))
	_, problems := LoadWorker(bad)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Error(), "WORKER_RECORD REFUSED: malformed", "the refusal is named")
	assert.Contains(t, problems[0].Error(), `unknown field "key_fil"`, "and still names the misspelled field")
}

// writeExit writes want in form via the reader's writer side.
func writeExit(t *testing.T, r *schemaread.Reader[exitDoc], want *exitDoc, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(want, form)
	require.NoError(t, err)
	return out
}

// writeDeclaredModel writes want in form via the reader's writer side.
func writeDeclaredModel(t *testing.T, r *schemaread.Reader[declaredModelDoc], want *declaredModelDoc, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(want, form)
	require.NoError(t, err)
	return out
}

// exitRefusal unwraps the refusal of a read.
func exitRefusal(t *testing.T, err error) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	return ref
}
