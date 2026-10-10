package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

const (
	testRedis = "127.0.0.1:6390"
	testHome  = "/home/ada"
)

// testNow is the fake clock every world reads: no test reads real time.
var testNow = time.Date(2026, 10, 6, 8, 10, 0, 0, time.UTC)

// world is a machine as the job steps see it through the tools: each field one
// dependency, true when it is there. The fake tools answer from it, and the fake shell
// (world.run) changes it only by the exact fix commands nova-doctor prints. Both read
// every nova command through parseArgv (argv_test.go) first, so neither accepts a line
// the real tool refuses. The session's evidence is nova-friend check's: the age of the
// last session pong ever recorded ("-" none), the age of the newest card finished, and
// the messages back; its verdict is judged over the --since the doctor passed.
type world struct {
	t                                                    *testing.T
	root                                                 string
	redisUp, loginOK, storeLogin, schema, applied, aclOK bool
	fnLoaded, swarmAgrees                                bool
	daemon, broken, delivered                            bool
	pongAge                                              string
	finishAge                                            time.Duration
	back, outbox                                         int
	calls                                                int // tool calls the doctor made
	writingCalls                                         int // writing argv observed in doctor probes, excluding caller repairs
	// Coordinator preflight. Zero values are a healthy seat except the bools set in
	// healthyWorld: schema 35/36, an empty config, a folder or queue write, friend rows,
	// a review/lander backup and a red release gate are what a case turns on.
	configEmpty                                     bool
	schemaHave, schemaWant                          int
	pushInstalled, busProven, sprintProven          bool
	folderWrite, queueWrite                         bool
	pushNonce                                       string
	installs, noncesIssued                          int
	ponged                                          string
	friends                                         []friendFact
	review, lander                                  int
	releaseRed                                      bool
	schemaOwner, seatDrift, serviceDown, machineRow bool
	// seatServer is the actor the running server reports when it differs from the seat's
	// holder (a server-actor-only drift): "" is no drift.
	seatServer string
}

// friendFact is one configured friend as the preflight's fakes report it.
// asserted is the row's own working count, which the doctor must not treat as work.
type friendFact struct {
	name, state, push string
	width, asserted   int
	running           []string
}

var allTools = []string{"nova-bus", "nova-config", "nova-friend", "nova-redis", "nova-sprint", "nova-swarm"}

func healthyWorld(t *testing.T) *world {
	w := &world{t: t, root: t.TempDir(), redisUp: true, loginOK: true, storeLogin: true, schema: true, applied: true,
		aclOK: true, fnLoaded: true, swarmAgrees: true, daemon: true, delivered: true,
		pongAge: "20s", finishAge: 8 * time.Minute, back: 2, outbox: 2,
		pushInstalled: true, busProven: true, sprintProven: true}
	require.NoError(t, os.MkdirAll(filepath.Join(w.root, "bin"), 0o755))
	for _, n := range allTools {
		w.install(n)
	}
	return w
}

func (w *world) install(tool string) {
	require.NoError(w.t, os.WriteFile(filepath.Join(w.root, "bin", tool), nil, 0o755))
}

func (w *world) uninstall(tool string) {
	require.NoError(w.t, os.Remove(filepath.Join(w.root, "bin", tool)))
}

// deaf is a session that answered nothing in its windows: its last pong pongAge ago ("-"
// none), its newest card finished two hours ago, and nothing back on the bus.
func (w *world) deaf(pongAge string) {
	w.pongAge, w.finishAge, w.back = pongAge, 2*time.Hour, 0
}

func (w *world) installed(tool string) bool {
	_, err := os.Stat(filepath.Join(w.root, "bin", tool))
	return err == nil
}

// exitErr is a tool's exit: its code and what it said on stderr.
type exitErr struct {
	code   int
	stderr string
}

func (e exitErr) Error() string { return e.stderr }
func (e exitErr) ExitCode() int { return e.code }

func (w *world) vars() map[string]string {
	env := map[string]string{"PATH": "bin", "HOME": testHome, "NOVA_REDIS_ADDR": testRedis, "NOVA_SPRINT_ACTOR": "ada"}
	if w.configEmpty {
		env["NOVA_SPRINT_ACTOR"] = "ada"
		env["NOVA_PG_DSN"] = "postgres://nova_config@127.0.0.1:5432/nova"
		env["NOVA_PG_PASSWORD_ENV"] = "NOVA_PG_CONFIG_PASSWORD"
	}
	return env
}

func (w *world) env() fakeEnv {
	return fakeEnv{
		env:   w.vars(),
		root:  w.root,
		clock: testNow,
		dial: func(addr string) error {
			if addr != testRedis || !w.redisUp {
				return errors.New("connection refused")
			}
			return nil
		},
		exec: w.exec,
	}
}

// exec is every tool the job steps call, answering as the real one does from the world.
func (w *world) exec(name string, args ...string) (string, error) {
	tool := filepath.Base(name)
	w.calls++
	if !w.installed(tool) {
		return "", &exec.Error{Name: tool, Err: exec.ErrNotFound}
	}
	if len(args) == 1 && args[0] == "version" { // the self check, which runs through the frame's Env
		return buildinfo.Line(tool, "v1.0.0"), nil
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = oneline.ShellWord(arg)
	}
	cmd := tool + " " + strings.Join(quoted, " ")
	a, refused := parseArgv(cmd)
	if refused != "" {
		w.t.Fatalf("the doctor ran a command the real tool refuses: %q: %s", cmd, refused)
	}
	if (a.verb == "nova-config login" && !a.has("check")) || (a.verb == "nova-config migrate" && !a.has("dry-run")) || a.verb == "nova-sprint seat install" || a.verb == "nova-redis fn load" || (a.verb == "nova-config apply" && !a.has("check")) {
		w.writingCalls++
	}

	switch a.verb {
	case "nova-redis acl check", "nova-redis fn check":
		if a.flags["redis"] != testRedis && a.flags["addr"] != testRedis {
			break
		}
		if a.verb == "nova-redis fn check" {
			if !w.fnLoaded {
				return "", exitErr{1, "FN CHECK MISSING library=nova"}
			}
			return "FN CHECK OK library=nova\n", nil
		}
		switch {
		case !w.redisUp:
			return "", exitErr{2, "ACL CHECK FAILED store=" + testRedis + " err=\"connection refused\""}
		case !w.loginOK:
			return "", exitErr{2, "ACL CHECK FAILED store=" + testRedis + " err=\"WRONGPASS\""}
		case !w.aclOK:
			// As the real tool: the drift is on stdout at exit 1, its remedy a command and then prose.
			return "NOTE ACL DEFAULT on=false nopass=false\nACL CHECK DRIFT users=4 differ=1 library=abc store=" + testRedis +
				" remedy=\"nova-redis acl apply --redis " + testRedis + " sets the users that differ\"\n", exitErr{1, ""}
		}
		return "NOTE ACL DEFAULT on=false nopass=false\nACL CHECK OK users=4 library=abc store=" + testRedis + "\n", nil
	case "nova-config login":
		if w.configEmpty {
			return "", exitErr{2, "nova-config login REFUSED: no login is recorded at /x; run: nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --actor <name>"}
		}
		if !w.storeLogin {
			return "", exitErr{2, "nova-config login REFUSED: no login is recorded at /x; run: nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --friend <actor>"}
		}
		return "LOGIN file=/x dsn=postgres://h/db resolves=yes\n", nil
	case "nova-config status":
		if w.schemaHave > 0 && w.schemaWant > w.schemaHave {
			return fmt.Sprintf("CONFIG STATUS pg=nova_admin@127.0.0.1:5432/nova schema=%d\n", w.schemaHave),
				exitErr{1, fmt.Sprintf("nova-config status REFUSED: schema config is at version %d and this binary carries %d; run: nova-update apply --version v9 && nova-sprint server switch", w.schemaHave, w.schemaWant)}
		}
		if !w.schema {
			return "CONFIG STATUS pg=h schema=0 redis=-\n", exitErr{1, "nova-config status REFUSED: schema config is not there yet; run: nova-config migrate"}
		}
		return "CONFIG STATUS pg=h schema=3\n", nil
	case "nova-config migrate":
		if w.schemaOwner {
			return "MIGRATE NOT-OWNED table=machine owner=nova_config role=nova_admin\n", exitErr{1, ""}
		}
		return "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova applied=0 dry_run=true ready=yes\n", nil
	case "nova-config apply":
		if !a.has("check") || a.flags["redis"] != testRedis {
			break
		}
		rev := "7"
		if !w.applied {
			rev = "6"
		}
		return "CONFIG CHECK kind=machine add=0 set=0 remove=0 rev=7 applied=7\nCONFIG CHECK kind=friend add=0 set=0 remove=0 rev=7 applied=" + rev + "\n", nil
	case "nova-swarm doctor":
		if !w.swarmAgrees {
			return "", exitErr{2, "DOCTOR DRIFT path=/usr/bin/nova-swarm\nDOCTOR REFUSED /usr/bin/nova-swarm shadows ~/.local/bin/nova-swarm"}
		}
		return "DOCTOR OK stamp=v1.0.0\n", nil
	case "nova-friend check":
		// The health check: never the delivery check, which the doctor only prints.
		if a.has("harness") || !a.has("json") || a.flags["redis"] != testRedis {
			break
		}
		since, err := time.ParseDuration(a.flags["since"])
		require.NoError(w.t, err, "the doctor passes the window: %q", cmd)
		if !slices.Equal(a.args, []string{"bob"}) && !(len(a.args) == 0 && a.flags["as"] == "ada") {
			break
		}
		rep := w.friendReport(since)
		b, err := json.Marshal(rep)
		require.NoError(w.t, err)
		if rep.Friends[0].Verdict.Verdict != "ok" {
			return string(b), exitErr{1, ""}
		}
		return string(b), nil
	case "nova-bus names":
		if a.flags["redis"] != testRedis {
			break
		}
		return w.busNames(), nil
	case "nova-sprint seat":
		if a.flags["actor"] != "ada" || a.flags["redis"] != testRedis {
			break
		}
		if w.seatDrift {
			return "SEAT holder=ada epoch=1 generation=1 record=ada DRIFT the key says wrong and the record ada: nova-sprint seat --repair --reason <text> (the holder or the owner)\n", exitErr{1, ""}
		}
		if w.seatServer != "" {
			return fmt.Sprintf("SEAT holder=ada epoch=1 generation=1 record=ada server=%s DRIFT the server runs as %s and the seat is ada's: change the server's NOVA_SPRINT_ACTOR=%s to NOVA_SPRINT_ACTOR=ada and restart it\n", w.seatServer, w.seatServer, w.seatServer), exitErr{1, ""}
		}
		return w.seatText()
	case "nova-sprint seat check":
		if w.serviceDown {
			return "MACHINERY inbox DOWN remedy=\"nova-sprint seat install --harness claude --target /home/ada/session --actor ada --redis 127.0.0.1:6390\"\n", exitErr{1, ""}
		}
		return "MACHINERY server OK\nMACHINERY inbox OK\nMACHINERY versions OK\nMACHINERY fleet DOWN held=1\nMACHINERY queue DOWN review=2\nMACHINERY push DOWN pending=true\nMACHINERY DOWN n=3\n", nil
	case "nova-sprint seat push":
		if a.flags["actor"] != "ada" || a.flags["redis"] != testRedis || len(a.args) > 0 {
			break
		}
		if !a.has("json") {
			return "PUSH DOWN remedy=\"nova-sprint seat install --harness claude --target /home/ada/session --actor ada --redis " + testRedis + "\"\n", exitErr{1, ""}
		}
		return w.pushText()
	case "nova-sprint view coordinator":
		if !a.has("all") || !a.has("json") || a.flags["actor"] != "ada" || a.flags["redis"] != testRedis {
			break
		}
		return w.coordJSON()
	case "nova-sprint view worker":
		if !a.has("json") || a.flags["actor"] != "ada" || a.flags["redis"] != testRedis || a.flags["as"] == "" {
			break
		}
		return w.workerJSON(a.flags["as"])
	case "nova-sprint release check":
		if a.flags["actor"] != "ada" || a.flags["redis"] != testRedis {
			break
		}
		return w.releaseText()
	}
	w.t.Fatalf("the doctor called a tool the fake does not know: %s", cmd)
	return "", nil
}

func (w *world) friendFacts() []friendFact {
	if w.friends != nil {
		return w.friends
	}
	return []friendFact{{name: "bob", state: "up", push: "proven", width: 8}}
}

func (w *world) busNames() string {
	facts := w.friendFacts()
	ada := "none"
	if w.busProven {
		ada = "proven"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "NAMES OK count=%d proven=0\n", 1+len(facts))
	fmt.Fprintf(&b, "NAMES NAME name=ada push=%s age=20s harness=grok\n", ada)
	for _, f := range facts {
		push := f.push
		if push == "" {
			push = "none"
		}
		fmt.Fprintf(&b, "NAMES NAME name=%s push=%s age=20s harness=grok\n", f.name, push)
	}
	return b.String()
}

func (w *world) seatText() (string, error) {
	if !w.pushInstalled {
		return "SEAT holder=- epoch=0 generation=0\n", exitErr{1, ""}
	}
	return "SEAT holder=ada epoch=1 generation=1\n", nil
}

func (w *world) pushText() (string, error) {
	status := map[string]any{"recorded": w.pushInstalled, "live": w.sprintProven && w.busProven, "proof": "none", "why": ""}
	if !w.pushInstalled {
		status["why"] = "ada has no push target recorded"
		b, _ := json.Marshal(status)
		return string(b), exitErr{1, ""}
	}
	if w.sprintProven && w.busProven {
		status["proof"] = "proven"
		b, _ := json.Marshal(status)
		return string(b), nil
	}
	if w.pushNonce != "" {
		status["proof"] = "pending"
		status["why"] = "the push check went into ada's grok session and no pong carrying it came back"
	} else {
		status["why"] = "no push check has been delivered into ada's grok session yet"
	}
	b, _ := json.Marshal(status)
	return string(b), exitErr{1, ""}
}

func (w *world) coordJSON() (string, error) {
	type row struct {
		K  string `json:"k"`
		St string `json:"st"`
		W  int    `json:"w"`
		Wd int    `json:"wd"`
	}
	doc := struct {
		View string `json:"view"`
		Rows []row  `json:"rows"`
		N    struct {
			Review int `json:"review"`
			Merge  int `json:"merge"`
		} `json:"n"`
	}{View: "coordinator"}
	for _, f := range w.friendFacts() {
		doc.Rows = append(doc.Rows, row{K: "f:" + f.name, St: f.state, W: f.asserted, Wd: f.width})
	}
	if w.machineRow {
		doc.Rows = append(doc.Rows, row{K: "m:bench", St: "up", W: 32, Wd: 32})
	}
	doc.N.Review, doc.N.Merge = w.review, w.lander
	b, err := json.Marshal(doc)
	require.NoError(w.t, err)
	return string(b) + "\n", nil
}

func (w *world) workerJSON(name string) (string, error) {
	var running []string
	for _, f := range w.friendFacts() {
		if f.name == name {
			running = f.running
		}
	}
	type card struct {
		ID string `json:"id"`
		St string `json:"st"`
	}
	doc := struct {
		View  string `json:"view"`
		As    string `json:"as"`
		Cards []card `json:"cards"`
	}{View: "worker", As: name, Cards: []card{}}
	for _, id := range running {
		doc.Cards = append(doc.Cards, card{ID: id, St: "working"})
	}
	b, err := json.Marshal(doc)
	require.NoError(w.t, err)
	return string(b) + "\n", nil
}

func (w *world) releaseText() (string, error) {
	if !w.releaseRed {
		return "RELEASE CHECK exact-head ok\nRELEASE CHECK landing-receipts ok\nRELEASE CHECK gates ok\nRELEASE CHECK version-order ok\nRELEASE OK checks=4\n", nil
	}
	return "RELEASE CHECK exact-head fail a cold read is not this preflight\nRELEASE CHECK landing-receipts fail native parallel receipts are not this preflight\nRELEASE CHECK gates fail the gate is red\nRELEASE CHECK version-order fail tools before sprint is not this preflight\nRELEASE NOT READY failed=4\n", exitErr{1, ""}
}

func (w *world) friendReport(since time.Duration) friend.CheckReport {
	f := friend.FriendCheck{Friend: "bob"}
	f.Daemon = friend.DaemonFacts{Friend: "bob", Agent: "loaded", PID: "42", Status: "ok", PongAge: w.pongAge, Presence: "up"}
	if !w.daemon {
		f.Daemon.Agent, f.Daemon.PID, f.Daemon.Status = "not-loaded", "-", "none"
	}
	f.Harness = friend.HarnessFacts{Friend: "bob", Harness: "claude", Route: "push", Last: "2026-10-06T08:00:00Z", LastExit: "0", Broken: "-", Reason: "-"}
	if w.delivered {
		f.Harness.Delivered = 3
	}
	if w.broken {
		f.Harness.Broken, f.Harness.Reason = "2026-10-06T07:00:00Z", "provider refused 3 turns"
	}
	f.Bus = friend.BusFacts{Friend: "bob", RealSince: w.back, LastReal: "-"}
	if w.back > 0 {
		f.Bus.LastReal = "2026-10-06T08:01:00Z"
	}
	f.Work = friend.WorkFacts{Friend: "bob", Outbox: w.outbox, NewestOutbox: "-", NewestAt: "-"}
	if w.outbox > 0 {
		f.Work.NewestOutbox, f.Work.NewestAt = "card-7", testNow.Add(-w.finishAge).Format(time.RFC3339)
	}
	// The verdict as nova-friend decides it over --since (docs/SPEC-FRIEND.md, "The
	// verdicts"): a delivery that succeeded with no session pong aged within the window
	// and no real message back is deaf.
	pong, err := time.ParseDuration(w.pongAge)
	verdict := "ok"
	switch {
	case w.broken:
		verdict = "broken"
	case w.delivered && w.back == 0 && (err != nil || pong > since):
		verdict = "deaf"
	case !w.delivered && w.back == 0:
		verdict = "silent"
	case !w.daemon:
		verdict = "down"
	}
	f.Verdict = friend.VerdictFacts{Friend: "bob", Verdict: verdict, Shown: "-", Why: verdict}
	s := friend.CheckSummary{Friends: 1}
	switch verdict {
	case "ok":
		s.OK = 1
	case "broken":
		s.Broken = 1
	case "deaf":
		s.Deaf = 1
	case "silent":
		s.Silent = 1
	default:
		s.Down = 1
	}
	return friend.CheckReport{Friends: []friend.FriendCheck{f}, Summary: s}
}

// installLine is a nova tool's install, from the module the doctor was built from.
var installLine = regexp.MustCompile(`^go install \S+/cmd/(nova-[a-z]+)@latest$`)

// run is the cold reader's shell: it runs a command exactly as printed, and knows only
// the commands a machine's owner has. A nova command goes through parseArgv first, so a
// line the real tool refuses (a flag after an argument, a required flag missing) fails
// here as it would there; a placeholder is a fix a stranger could not run.
func (w *world) run(cmd string) {
	if strings.Contains(cmd, "<") {
		w.t.Fatalf("the fix has a placeholder the cold reader cannot fill: %q", cmd)
	}
	if m := installLine.FindStringSubmatch(cmd); m != nil {
		w.install(m[1])
		return
	}
	if cmd == "install -m 0755 "+testHome+"/.local/bin/nova-swarm bin/nova-swarm" {
		w.swarmAgrees = true
		return
	}
	a, refused := parseArgv(cmd)
	if refused != "" {
		w.t.Fatalf("the fix is a command the real tool refuses: %q: %s", cmd, refused)
	}
	f := a.flags
	switch {
	case a.verb == "nova-redis serve" && f["bind"] == "127.0.0.1" && f["port"] == "6390" && f["dir"] == testHome+"/nova/stores/redis":
		w.redisUp = true
	case a.verb == "nova-redis fn load" && f["redis"] == testRedis:
		w.fnLoaded = true
	case a.verb == "nova-redis acl apply" && f["redis"] == testRedis:
		w.aclOK = true
	case a.verb == "nova-config migrate":
		w.schema = true
	case a.verb == "nova-config login" && !a.has("check"):
		if f["store"] != "/secrets" || f["as"] != "store-seat" || f["key"] != "/keys/store" || f["sops"] != "/bin/sops" ||
			f["secret"] != "NOVA_PG_CONFIG_PASSWORD" || f["dsn"] != "postgres://nova_config@127.0.0.1:5432/nova" || f["actor"] != "ada" {
			w.t.Fatalf("store login did not name the secret source: %q", cmd)
		}
		w.configEmpty = false
	case a.verb == "nova-config apply" && !a.has("check") && f["redis"] == testRedis:
		w.applied = true
	case a.verb == "nova-friend install" && f["as"] == "bob" && f["harness"] == "claude" && f["dir"] == "/home/bob" && f["redis"] == testRedis &&
		f["config-dir"] == "/home/bob/.claude":
		w.daemon, w.broken = true, false
	case a.verb == "nova-friend ping" && f["as"] == "ada" && f["to"] == "bob" && f["redis"] == testRedis:
		w.delivered = true
	case a.verb == "nova-sprint seat install" && f["dry-run"] == "true":
		if f["harness"] != "grok" || f["target"] != testHome+"/session" || f["actor"] != "ada" || f["redis"] != testRedis ||
			f["config-seat"] != "ada" || f["config-dsn"] != "postgres://nova_config@127.0.0.1:5432/nova" || f["config-password-env"] != "NOVA_PG_CONFIG_PASSWORD" {
			w.t.Fatalf("the dry run is not the empty config's secret-backed command: %q", cmd)
		}
	case a.verb == "nova-sprint seat install":
		if f["harness"] != "claude" || f["target"] != testHome+"/session" || f["actor"] != "ada" || f["redis"] != testRedis {
			w.t.Fatalf("seat install is not the seat's own: %q", cmd)
		}
		if !w.pushInstalled {
			w.installs++
		}
		w.pushInstalled = true
		if w.pushNonce == "" {
			w.pushNonce = "check-1"
			w.noncesIssued++
		}
	case a.verb == "nova-sprint seat pong":
		if f["actor"] != "ada" || f["redis"] != testRedis || len(a.args) != 1 || a.args[0] == "" || a.args[0] != w.pushNonce {
			w.t.Fatalf("the pong is not the outstanding nonce: %q nonce=%s", cmd, w.pushNonce)
		}
		if w.ponged == a.args[0] {
			w.t.Fatalf("nonce %s was answered twice", a.args[0])
		}
		w.ponged = a.args[0]
		w.sprintProven, w.busProven = true, true
	case a.verb == "nova-sprint inbox":
		if f["push"] != "seat" || !a.has("wait") || f["actor"] != "ada" || f["redis"] != testRedis {
			w.t.Fatalf("the inbox push is not the seat's: %q", cmd)
		}
		if w.pushInstalled && w.pushNonce == "" {
			w.pushNonce = "check-1"
			w.noncesIssued++
		}
	case a.verb == "nova-friend check" && a.has("harness"):
		// The delivery check: --as is the friend itself, its pong goes to the coordinator.
		if f["as"] != "bob" || f["harness"] != "claude" || f["dir"] != "/home/bob" || f["redis"] != testRedis || f["to"] != "ada" || len(a.args) > 0 {
			w.t.Fatalf("the delivery check is not bob's own, to ada: %q", cmd)
		}
		w.pongAge = "5s"
	default:
		w.t.Fatalf("the fix is not a command the cold reader can run as printed: %q", cmd)
	}
}

// fixesParse is every fix line in a doctor's output that names a nova tool, each read
// as its tool would: none may be a line the real tool refuses.
func fixesParse(t *testing.T, lines []string) {
	t.Helper()
	for _, l := range lines {
		fix, ok := "", false
		if _, fix, ok = strings.Cut(l, " fix: "); !ok {
			_, fix, ok = strings.Cut(l, " next: ")
		}
		if !ok || !strings.HasPrefix(fix, "nova-") {
			continue
		}
		_, refused := parseArgv(fix)
		assert.Empty(t, refused, "a fix the real tool refuses: %q", fix)
	}
}

// doctor runs nova-doctor as a stranger would and returns the exit and the lines.
func (w *world) doctor(args ...string) (int, []string) {
	var out, errb bytes.Buffer
	code := Main(NewRegistry(), w.env(), "", args, strings.NewReader(""), &out, &errb)
	require.Empty(w.t, errb.String())
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	fixesParse(w.t, lines)
	return code, lines
}

// jobArgs are the flags each job runs with in these tests.
var jobArgs = map[string][]string{
	"local-notes": {"--job", "local-notes", "--redis-secrets", "/secrets", "--redis-seat", "store-seat", "--redis-key", "/keys/store", "--redis-sops", "/bin/sops", "--redis-secret", "NOVA_REDIS_PASSWORD"},
	"messaging":   {"--job", "messaging"},
	"friend":      {"--job", "friend", "--as", "bob", "--dir", "/home/bob", "--config-dir", "/home/bob/.claude"},
	"worker":      {"--job", "worker", "--redis-secrets", "/secrets", "--redis-seat", "store-seat", "--redis-key", "/keys/store", "--redis-sops", "/bin/sops", "--redis-secret", "NOVA_REDIS_PASSWORD"},
	"coordinator": {"--job", "coordinator", "--as", "ada", "--harness", "claude", "--dir", "/home/ada/session", "--redis-secrets", "/secrets", "--redis-seat", "store-seat", "--redis-key", "/keys/store", "--redis-sops", "/bin/sops", "--redis-secret", "NOVA_REDIS_PASSWORD"},
}

// The acceptance: one dependency is deliberately missing; nova-doctor --job names it as
// the first missing step, every step before it ok and every one after it blocked, and
// prints the one command that fixes it. A cold reader that knows nothing else runs that
// command as printed and nova-doctor is then ready. The test records the calls: doctor
// runs, repairs, and the tool calls the doctor itself made.
func TestDoctorNamesTheFirstMissingDependencyAndItsFix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		job, step, why string
		breakIt        func(w *world)
		extra          []string // flags after the job's own
	}{
		{"local-notes", "redis-reachable", "", func(w *world) { w.redisUp = false }, nil},
		{"local-notes", "binaries", "", func(w *world) { w.uninstall("nova-redis"); w.redisUp = true }, nil},
		{"messaging", "binaries", "", func(w *world) { w.uninstall("nova-bus") }, nil},
		{"worker", "redis-functions", "", func(w *world) { w.fnLoaded = false }, nil},
		{"worker", "swarm-binary", "", func(w *world) { w.swarmAgrees = false }, nil},
		{"coordinator", "config-schema", "", func(w *world) { w.schema = false }, nil},
		{"coordinator", "config-applied", "", func(w *world) { w.applied = false }, nil},
		{"coordinator", "redis-acl", "", func(w *world) { w.aclOK = false }, nil},
		{"friend", "daemon-running", "", func(w *world) { w.daemon = false }, nil},
		{"friend", "harness-responsive", "", func(w *world) { w.broken = true }, nil},
		{"friend", "message-delivered", "", func(w *world) { w.delivered = false }, nil},
		{"friend", "session-receipt", "", func(w *world) { w.deaf("-") }, nil},
		// The receipt is nova-friend's verdict over --since, never a pong of any age: a pong
		// 72 hours old is outside the default 24h window, and one 11 minutes old is outside
		// a 10m window the doctor is given and passes on.
		{"friend", "session-receipt", "a pong 72 hours old that nova-friend calls deaf", func(w *world) { w.deaf("72h0m0s") }, nil},
		{"friend", "session-receipt", "a pong 11 minutes old over --since 10m", func(w *world) { w.deaf("11m0s") }, []string{"--since", "10m"}},
	}
	for _, tc := range cases {
		name := tc.job + "/" + tc.step
		if tc.why != "" {
			name += "/" + tc.why
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			tc.breakIt(w)
			if tc.step == "binaries" && tc.job == "local-notes" {
				// nova-redis gone: the login step calls it first, so the first missing is
				// named there, by the same install line.
				tc.step = "redis-login"
			}
			args := append(slices.Clone(jobArgs[tc.job]), tc.extra...)
			code, lines := w.doctor(args...)
			assert.Equal(t, 2, code, lines)
			names := stepNames(tc.job)
			at := slices.Index(names, tc.step)
			require.GreaterOrEqual(t, at, 0, "%s is a step of %s", tc.step, tc.job)
			require.Len(t, lines, len(names)+1, "one line per step and the summary: %q", lines)
			for i, n := range names {
				want := map[bool]string{true: "ok", false: "blocked"}[i < at]
				if i == at {
					want = "fail"
				}
				assert.True(t, strings.HasPrefix(lines[i], "DOCTOR "+n+" "+want+" "), "step %d: want %s %s, got %q", i, n, want, lines[i])
			}
			summary := lines[len(lines)-1]
			require.Contains(t, summary, "first_missing="+tc.step+" ", summary)

			// The cold reader: the next command, and nothing else, from the output.
			_, next, ok := strings.Cut(summary, " next: ")
			require.True(t, ok, summary)
			doctorRuns, repairs := 1, 0
			for code != 0 {
				require.Less(t, repairs, 3, "the doctor did not converge: %q", lines)
				w.run(next)
				repairs++
				code, lines = w.doctor(args...)
				doctorRuns++
				_, next, _ = strings.Cut(lines[len(lines)-1], " next: ")
			}
			assert.Equal(t, 1, repairs, "one missing dependency is one repair")
			assert.Equal(t, 2, doctorRuns)
			assert.Contains(t, lines[len(lines)-1], "DOCTOR job="+tc.job+" ready ")
			t.Logf("calls: job=%s missing=%s doctor_runs=%d repairs=%d tool_calls=%d", tc.job, tc.step, doctorRuns, repairs, w.calls)
			assert.LessOrEqual(t, w.calls, 2*(len(names)+len(allTools)), "each tool call is made once per run")
		})
	}

	t.Run("two missing are fixed in dependency order", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.redisUp, w.fnLoaded = false, false
		var order []string
		for range 3 {
			code, lines := w.doctor(jobArgs["worker"]...)
			if code == 0 {
				break
			}
			summary := lines[len(lines)-1]
			step := strings.Fields(strings.SplitN(summary, "first_missing=", 2)[1])[0]
			order = append(order, step)
			_, next, _ := strings.Cut(summary, " next: ")
			w.run(next)
		}
		assert.Equal(t, []string{"redis-reachable", "redis-functions"}, order)
	})

	// Coordinator preflight (Glenn, 2026-10-08). Each case is the same doctor: one next
	// command, fake clock and servers, no live service. The cold reader is the world's run.
	t.Run("empty config names a secret-backed login and the doctor writes nothing", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.configEmpty = true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		summary := lines[len(lines)-1]
		assert.Contains(t, summary, "first_missing=store-login ")
		_, next, ok := strings.Cut(summary, " next: ")
		require.True(t, ok, summary)
		assert.Equal(t, 1, strings.Count(strings.Join(lines, "\n"), " next: "))
		want := "nova-config login --store /secrets --as store-seat --key /keys/store --sops /bin/sops --secret NOVA_PG_CONFIG_PASSWORD --dsn postgres://nova_config@127.0.0.1:5432/nova --actor ada"
		assert.Equal(t, want, next)
		_, refused := parseArgv(next)
		assert.Empty(t, refused, next)
		assert.NotContains(t, next, "hunter2")
		assert.Equal(t, 0, w.installs)
		assert.True(t, w.configEmpty)
		assert.Zero(t, w.writingCalls, "the doctor probes perform no write")
		assert.True(t, w.schema)
		w.run(next)
		code, lines = w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 0, code, lines)
		assert.False(t, w.configEmpty)
		assert.Equal(t, 0, w.installs)
	})

	t.Run("owner migration precedes switch", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.schemaHave, w.schemaWant, w.schemaOwner = 35, 36, true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code)
		assert.Contains(t, lines[len(lines)-1], "nova-config migrate --pg postgres://nova_config@127.0.0.1:5432/nova")
	})
	t.Run("seat drift is not a generation receipt", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.seatDrift = true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code)
		assert.Contains(t, lines[len(lines)-1], "first_missing=seat-agreement")
		assert.Contains(t, lines[len(lines)-1], "--repair")
	})
	t.Run("a server actor drift names a restart, not a repair or an instruction", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.seatServer = "bob"
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "DOCTOR seat-agreement fail ")
		assert.Contains(t, lines[len(lines)-1], "first_missing=seat-agreement ")
		_, next, ok := strings.Cut(lines[len(lines)-1], " next: ")
		require.True(t, ok, lines[len(lines)-1])
		assert.Equal(t, "nova-sprint install server --listen <address:port> --redis "+testRedis+" --actor ada", next)
		assert.NotContains(t, next, "--repair")
		_, refused := parseArgv(next)
		assert.Empty(t, refused, next)
	})
	t.Run("missing installed service precedes push", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.serviceDown = true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code)
		assert.Contains(t, lines[len(lines)-1], "first_missing=seat-service")
		assert.Contains(t, lines[len(lines)-1], "seat install")
	})
	t.Run("fleet row width does not assert working jobs", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.machineRow = true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 0, code)
		assert.Contains(t, strings.Join(lines, "\n"), "fleet m:bench state=up width=32 running=- working=0")
	})

	t.Run("schema 35 and binary 36 migrate before a binary switch", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.schemaHave, w.schemaWant = 35, 36
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "DOCTOR config-schema fail ")
		assert.Contains(t, out, "DOCTOR binaries blocked ")
		_, next, ok := strings.Cut(lines[len(lines)-1], " next: ")
		require.True(t, ok)
		assert.Equal(t, "nova-config migrate", next)
		assert.NotContains(t, next, "install")
		assert.NotContains(t, next, "switch")
		assert.NotContains(t, next, "nova-update")
	})

	t.Run("a folder or queue write without a root answer stays pending", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.folderWrite, w.queueWrite = true, true
		w.busProven, w.sprintProven = false, false
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "DOCTOR push-roundtrip fail pending: no root bus and sprint answer")
		_, next, ok := strings.Cut(lines[len(lines)-1], " next: ")
		require.True(t, ok, lines[len(lines)-1])
		assert.Contains(t, next, "nova-sprint seat install --harness claude ")
		w.run(next)
		assert.False(t, w.busProven)
		assert.False(t, w.sprintProven)
		code, lines = w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		assert.Contains(t, lines[len(lines)-1], "first_missing=push-roundtrip ")
	})

	t.Run("an available friend missing a push fails and a held friend is capacity", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.friends = []friendFact{
			{name: "bob", state: "up", push: "none", width: 8, asserted: 8},
			{name: "sam", state: "held", push: "none", width: 32, asserted: 32},
		}
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "DOCTOR push-roundtrip ok ")
		assert.Contains(t, out, "DOCTOR friend-capacity fail ")
		assert.Contains(t, out, "available bob has no push receipt")
		assert.Contains(t, out, "sam state=held")
		assert.Contains(t, out, "capacity warning: reduced capacity, not a startup failure")
		assert.NotContains(t, out, "DOCTOR friend-capacity fail sam")
		_, next, ok := strings.Cut(lines[len(lines)-1], " next: ")
		require.True(t, ok)
		assert.Equal(t, "nova-doctor --job friend --as bob --redis "+testRedis, next)
	})

	t.Run("a restart resumes at the missing push without a new nonce or a second install", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.pushInstalled = false
		w.busProven, w.sprintProven = false, false
		w.folderWrite = true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		_, next, ok := strings.Cut(lines[len(lines)-1], " next: ")
		require.True(t, ok)
		assert.Contains(t, next, "nova-sprint seat install ")
		assert.NotContains(t, next, "--dry-run")
		assert.NotContains(t, next, "check-1")
		w.run(next)
		assert.Equal(t, 1, w.installs)
		assert.Equal(t, 1, w.noncesIssued)
		assert.Equal(t, "check-1", w.pushNonce)

		code, lines = w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "DOCTOR redis-reachable ok ")
		assert.Contains(t, out, "DOCTOR config-schema ok ")
		assert.Contains(t, lines[len(lines)-1], "first_missing=push-roundtrip ")
		_, next, ok = strings.Cut(lines[len(lines)-1], " next: ")
		require.True(t, ok)
		assert.Equal(t, "nova-sprint seat install --harness claude --target /home/ada/session --actor ada --redis "+testRedis, next)
		assert.NotContains(t, next, "--sent")
		assert.NotContains(t, next, "check-1")
		w.run(next)
		assert.Equal(t, "check-1", w.pushNonce, "the delivered check has a nonce only the session sees")

		code, again := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 2, code, again)
		_, next2, ok := strings.Cut(again[len(again)-1], " next: ")
		require.True(t, ok)
		assert.Equal(t, next, next2)
		assert.Equal(t, 1, w.installs)
		assert.Equal(t, 1, w.noncesIssued)
		assert.Equal(t, "", w.ponged)

		w.run("nova-sprint seat pong --actor ada --redis " + testRedis + " check-1")
		assert.Equal(t, "check-1", w.ponged)
		assert.Equal(t, 1, w.noncesIssued)
		code, lines = w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 0, code, lines)
		assert.Contains(t, lines[len(lines)-1], "DOCTOR job=coordinator ready ")
	})

	t.Run("widths 8 and 32 with no running jobs stay at zero working", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.friends = []friendFact{
			{name: "bob", state: "up", push: "proven", width: 8, asserted: 8},
			{name: "sam", state: "up", push: "proven", width: 32, asserted: 32},
		}
		w.review, w.lander = 40, 12
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 0, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "bob state=up width=8 running=- working=0 push=proven")
		assert.Contains(t, out, "sam state=up width=32 running=- working=0 push=proven")
		assert.NotContains(t, out, "working=8")
		assert.NotContains(t, out, "working=32")
		assert.Contains(t, out, "DOCTOR runtime-progress warn runtime warning: review=40 lander=12")
		assert.Contains(t, lines[len(lines)-1], "DOCTOR job=coordinator ready ")
		assert.NotContains(t, lines[len(lines)-1], "first_missing=")
	})

	t.Run("a red release gate does not falsify a healthy coordination preflight", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.releaseRed = true
		code, lines := w.doctor(jobArgs["coordinator"]...)
		assert.Equal(t, 0, code, lines)
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "DOCTOR push-roundtrip ok ")
		assert.Contains(t, out, "DOCTOR friend-capacity ok ")
		assert.Contains(t, out, "DOCTOR runtime-progress ok ")
		assert.Contains(t, out, "DOCTOR release-blockers warn release blockers stay separate from coordination readiness: RELEASE NOT READY failed=4")
		assert.NotContains(t, out, "DOCTOR release-blockers fail")
		assert.Contains(t, lines[len(lines)-1], "DOCTOR job=coordinator ready ")
	})
}

func stepNames(job string) []string {
	var out []string
	for _, s := range JobSteps(job) {
		out = append(out, s.Name)
	}
	return out
}

// The friend job reports the five facts apart: the daemon running, the harness
// responsive, a message delivered, the session's receipt and a card completed, each its
// own step and line; a daemon that runs says nothing of the session.
func TestDoctorFriendJobKeepsTheFiveFactsApart(t *testing.T) {
	t.Parallel()
	w := healthyWorld(t)
	code, lines := w.doctor(jobArgs["friend"]...)
	assert.Equal(t, 0, code, lines)
	got := map[string]string{}
	for _, l := range lines[:len(lines)-1] {
		f := strings.Fields(l)
		got[f[1]] = strings.Join(f[3:], " ")
	}
	assert.Contains(t, got["daemon-running"], "the daemon is running")
	assert.Contains(t, got["harness-responsive"], "harness takes turns")
	assert.Contains(t, got["message-delivered"], "3 messages delivered")
	assert.Contains(t, got["session-receipt"], "the session answered")
	assert.Contains(t, got["card-completion"], "newest=card-7")
	assert.Equal(t, 2+len(allTools), w.calls, "one friend check, one acl check, and the self version reads")

	t.Run("a running daemon with a silent session is not ready", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.deaf("-")
		code, lines := w.doctor(jobArgs["friend"]...)
		assert.Equal(t, 2, code)
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR daemon-running ok ")
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR session-receipt fail deaf: nova-friend verdict=deaf over 24h0m0s")
	})
	// The session's receipt is nova-friend's own verdict over the --since window
	// (docs/SPEC-FRIEND.md, "The verdicts"), never a pong of any age judged here: a pong
	// inside the window or a real message back is a receipt, and past both it is deaf,
	// with the delivery check as the fix.
	for _, tc := range []struct {
		name, pongAge string
		back          int
		extra         []string
		want          string
	}{
		{"a pong 11 minutes old inside the default 24h", "11m0s", 0, nil, "ok the session answered: nova-friend verdict=ok over 24h0m0s"},
		{"a pong 11 minutes old outside --since 10m", "11m0s", 0, []string{"--since", "10m"}, "fail deaf: nova-friend verdict=deaf over 10m0s"},
		{"a pong 72 hours old and nothing back", "72h0m0s", 0, nil, "fail deaf: nova-friend verdict=deaf over 24h0m0s"},
		{"a pong 72 hours old with messages back", "72h0m0s", 2, nil, "ok the session answered: nova-friend verdict=ok"},
		{"no pong ever and nothing back", "-", 0, nil, "fail deaf: nova-friend verdict=deaf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			w.pongAge, w.back = tc.pongAge, tc.back
			code, lines := w.doctor(append(slices.Clone(jobArgs["friend"]), tc.extra...)...)
			out := strings.Join(lines, "\n")
			assert.Contains(t, out, "DOCTOR session-receipt "+tc.want, out)
			if strings.HasPrefix(tc.want, "fail") {
				assert.Equal(t, 2, code, out)
				assert.Contains(t, out, "fix: nova-friend check --as bob --harness claude --dir /home/bob --redis "+testRedis+" --to ada", out)
			} else {
				assert.Equal(t, 0, code, out)
			}
		})
	}
	t.Run("no card completed yet is said, and is not a failure", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.outbox = 0
		code, lines := w.doctor(jobArgs["friend"]...)
		assert.Equal(t, 0, code)
		assert.Contains(t, strings.Join(lines, "\n"), "DOCTOR card-completion ok no card completed yet")
	})
}

// Every job's steps are in dependency order and begin at connectivity; --json is the
// same report, bounded; a name that is no job, or a job flag without --job, is refused.
func TestDoctorJobShapeAndRefusals(t *testing.T) {
	t.Parallel()
	for _, j := range Jobs {
		s := JobSteps(j)
		require.NotEmpty(t, s, j)
		assert.Equal(t, Connectivity, s[0].Stage, j)
		assert.True(t, slices.IsSortedFunc(s, func(a, b Step) int { return int(a.Stage) - int(b.Stage) }), j)
	}
	for n, s := range steps {
		assert.Equal(t, n, s.Name)
		assert.NotEmpty(t, s.Stage.String(), n)
	}

	w := healthyWorld(t)
	w.redisUp = false
	var out, errb bytes.Buffer
	code := Main(NewRegistry(), w.env(), "", []string{"--job", "coordinator", "--as", "ada", "--harness", "claude", "--dir", "/home/ada/session", "--json"}, strings.NewReader(""), &out, &errb)
	assert.Equal(t, 2, code)
	assert.Less(t, out.Len(), 4096, "the JSON is bounded")
	var rep JobReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &rep), out.String())
	assert.Equal(t, "redis-reachable", rep.FirstMissing)
	assert.True(t, strings.HasPrefix(rep.Next, "nova-doctor --job coordinator"), rep.Next)
	assert.Contains(t, rep.Next, "--redis-secrets", "missing authentication metadata is requested before a launch")
	assert.False(t, rep.Ready)
	assert.Len(t, rep.Steps, len(JobSteps("coordinator")))
	assert.Equal(t, Blocked, rep.Steps[1].Status)

	for _, args := range [][]string{{"--job", "nope"}, {"--as", "bob"}, {"--since", "1h"}, {"--job", "friend", "--local"}} {
		out.Reset()
		errb.Reset()
		code := Main(NewRegistry(), w.env(), "", args, strings.NewReader(""), &out, &errb)
		assert.Equal(t, 2, code, args)
		assert.Empty(t, out.String(), args)
		assert.NotEmpty(t, errb.String(), args)
	}
}

func TestRemedyAndClip(t *testing.T) {
	t.Parallel()
	for said, want := range map[string]string{
		`X FAILED remedy="nova-redis acl apply --redis a:1"`:               "nova-redis acl apply --redis a:1",
		"nova-config status REFUSED: no schema; run: nova-config migrate":  "nova-config migrate",
		"nova-config status REFUSED: bad flag; run: nova-config status -h": "def",
		"something else":  "def",
		"remedy=rm -rf /": "def",
		"no login; run: nova-config logout, then nova-config login again":                                 "def",
		"no password; run: nova-config login --check, then nova-config login again or nova-config logout": "def",
		"no login; run: nova-config login from the command line":                                          "def",
		"no password; run: nova-config login again with the store, seat, key and secret that hold it":     "def",
	} {
		assert.Equal(t, want, remedy(said, "def"), said)
	}
	long := strings.Repeat("é", 300)
	c := clip(long, maxEvidence)
	assert.LessOrEqual(t, len(c), maxEvidence)
	assert.True(t, strings.HasSuffix(c, "..."))
	assert.Equal(t, "short", clip("short", 10))
	assert.Equal(t, fmt.Sprint(Blocked), "blocked")
}

// Repair commands preserve the caller's directory and accept the real Redis bind grammar.
func TestDoctorRepairsPreserveTheirArguments(t *testing.T) {
	t.Parallel()
	t.Run("localhost is an IP bind", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.redisUp = false
		rep, err := RunJob(context.Background(), w.env(), JobInput{Job: "local-notes", Redis: "localhost:6390", RedisLogin: secrets.Login{Store: "/secrets", As: "store-seat", Key: "/keys/store", Sops: "/bin/sops", Name: "NOVA_REDIS_PASSWORD"}}, false)
		require.NoError(t, err)
		a, refused := parseArgv(rep.Next)
		assert.Empty(t, refused, rep.Next)
		assert.Equal(t, "127.0.0.1", a.flags["bind"])
	})
	for _, dir := range []string{"/home/example/work  trees", "/home/example/" + strings.Repeat("long-directory/", 24)} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			w.daemon = false
			rep, err := RunJob(context.Background(), w.env(), JobInput{Job: "friend", Redis: testRedis, As: "bob", Dir: dir, Harness: "claude", ConfigDir: dir + "/.claude"}, false)
			require.NoError(t, err)
			a, refused := parseArgv(rep.Next)
			assert.Empty(t, refused, rep.Next)
			assert.Equal(t, dir, a.flags["dir"], "the next command keeps the complete directory")
			assert.Equal(t, dir+"/.claude", a.flags["config-dir"])
			assert.Equal(t, w.calls, rep.Calls, "all tool invocations count, including self")
		})
	}
}

func TestDoctorRefusesANonpositiveReceiptWindow(t *testing.T) {
	t.Parallel()
	for _, since := range []string{"0s", "-1s"} {
		t.Run(since, func(t *testing.T) {
			t.Parallel()
			w := healthyWorld(t)
			var out, errb bytes.Buffer
			code := Main(NewRegistry(), w.env(), "", []string{"--job", "friend", "--since", since}, strings.NewReader(""), &out, &errb)
			assert.Equal(t, 2, code)
			assert.Contains(t, errb.String(), "--since wants a positive duration")
			assert.Equal(t, 0, w.calls, "invalid receipt windows read no dependencies")
		})
	}
}

func TestDoctorNeverTruncatesARepairCommand(t *testing.T) {
	t.Parallel()
	w := healthyWorld(t)
	w.daemon = false
	_, err := RunJob(context.Background(), w.env(), JobInput{Job: "friend", Redis: testRedis, As: "bob", Dir: "/" + strings.Repeat("d", maxFix), Harness: "claude"}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repair exceeds 4096 bytes")
}

// Missing inputs produce an explicit next diagnostic command instead of a serve
// or apply invocation the real tool refuses (SPEC-DOCTOR, Jobs).
func TestDoctorRequestsUnknownRepairInputs(t *testing.T) {
	t.Parallel()
	t.Run("Redis login", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.redisUp = false
		rep, err := RunJob(context.Background(), w.env(), JobInput{Job: "local-notes", Redis: testRedis}, false)
		require.NoError(t, err)
		a, refused := parseArgv(rep.Next)
		assert.Empty(t, refused)
		assert.Equal(t, "nova-doctor run", a.verb)
		for _, flag := range []string{"redis-secrets", "redis-seat", "redis-key", "redis-sops", "redis-secret"} {
			assert.True(t, a.has(flag), rep.Next)
		}
		assert.Equal(t, 0, w.calls)
	})
	t.Run("apply actor", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		e := w.env()
		delete(e.env, "NOVA_SPRINT_ACTOR")
		rep, err := RunJob(context.Background(), e, JobInput{Job: "coordinator", Redis: testRedis}, false)
		require.NoError(t, err)
		assert.Equal(t, "config-applied", rep.FirstMissing)
		assert.Contains(t, rep.Next, "--as")
		assert.Equal(t, 3, w.calls, "the invalid apply command is never executed")
	})
	t.Run("missing home", func(t *testing.T) {
		t.Parallel()
		w := healthyWorld(t)
		w.redisUp = false
		e := w.env()
		delete(e.env, "HOME")
		rep, err := RunJob(context.Background(), e, JobInput{Job: "local-notes", Redis: testRedis, RedisLogin: secrets.Login{Store: "/secrets", As: "seat", Key: "/key", Sops: "/sops", Name: "NOVA_REDIS_PASSWORD"}}, false)
		require.NoError(t, err)
		a, refused := parseArgv(rep.Next)
		assert.Empty(t, refused)
		assert.Equal(t, "nova-doctor run", a.verb)
		assert.True(t, filepath.IsAbs(a.flags["redis-dir"]))
	})
}

func TestDoctorRefusesPartialRedisLoginMetadata(t *testing.T) {
	t.Parallel()
	w := healthyWorld(t)
	var out, stderr bytes.Buffer
	code := Main(NewRegistry(), w.env(), "", []string{"--job", "local-notes", "--redis-secrets", "/secrets"}, strings.NewReader(""), &out, &stderr)
	assert.Equal(t, 2, code)
	for _, flag := range []string{"redis-seat", "redis-key", "redis-sops", "redis-secret"} {
		assert.Contains(t, stderr.String(), "--"+flag)
	}
	assert.Equal(t, 0, w.calls)
}
