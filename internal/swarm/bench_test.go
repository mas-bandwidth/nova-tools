package swarm

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeBenchTSV(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The seven columns parse, a "-" cores row is no pinning, and every table refusal
// above names the row that failed.
func TestBenchTableParsed(t *testing.T) {
	windowsIsNotABench(t)
	p := writeBenchTSV(t, "name\thost\troot\tcores\tharness\tauth\twall\n"+
		"b2\tb2\t/home/me/swarm\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone\n"+
		"mac\tmac\t/Users/me/swarm\t-\t/Users/me/bin/opencode\t/Users/me/.config/nova/auth\tsandbox\n")
	table, err := LoadBenchTable(p)
	if err != nil {
		t.Fatalf("a valid table parses: %v", err)
	}
	if len(table) != 2 {
		t.Fatalf("want 2 rows, got %d", len(table))
	}
	if table[0].Name != "b2" || table[0].Host != "b2" || table[0].Cores != "1-15" || table[0].Wall != "none" {
		t.Errorf("row 1 wrong: %+v", table[0])
	}
	if table[1].Cores != "-" || table[1].Pinned() {
		t.Errorf("row 2 is a - cores row and does not pin: %+v", table[1])
	}
	if table[0].Pinned() != true {
		t.Errorf("a list cores row pins")
	}

	refusals := []string{
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\trelative/swarm\t1-15\t/h\t/a\tnone\n",                     // relative root
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\trel-h\t/a\tnone\n",                           // relative harness
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\trel-a\tnone\n",                           // relative auth
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\tx-y\t/h\t/a\tnone\n",                               // cores list does not parse
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\t/a\twall\n",                              // wall neither word
		"name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\t/a\tnone\nb9\tb9\t/r\t-\t/h\t/a\tnone\n", // name used once
	}
	for i, body := range refusals {
		if i == len(refusals)-1 {
			body = "name\thost\troot\tcores\tharness\tauth\twall\nb2\tb2\t/root\t1-15\t/h\t/a\tnone\nb2\tb2\t/r\t-\t/h\t/a\tnone\n"
		}
		rp := writeBenchTSV(t, body)
		if _, err := LoadBenchTable(rp); err == nil {
			t.Errorf("case %d: expected a refusal", i)
		}
	}
}

// A cores column parses into sorted cores, and "-" into none.
func TestCoresList(t *testing.T) {
	got, err := CoresList("1-15")
	if err != nil || len(got) != 15 || got[0] != 1 || got[14] != 15 {
		t.Fatalf("1-15 parses to 15 cores: %v %v", got, err)
	}
	got, err = CoresList("2,4,6")
	if err != nil || len(got) != 3 || got[0] != 2 || got[2] != 6 {
		t.Fatalf("2,4,6 parses to three cores: %v %v", got, err)
	}
	got, err = CoresList("-")
	if err != nil || got != nil {
		t.Fatalf("- is no pinning: %v %v", got, err)
	}
	if _, err := CoresList("5-2"); err == nil {
		t.Error("a reversed range refuses")
	}
}

// A bench name on the bus's friends list is refused, and nothing is written to the
// bus: the check only reads the roster.
func TestBenchNameIsNotAFriend(t *testing.T) {
	busDir := t.TempDir()
	roster := busDir + "/participants.json"
	if err := os.WriteFile(roster, []byte(`{"participants":[{"name":"alice"},{"name":"bob"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	friends, err := FriendNames(busDir)
	if err != nil {
		t.Fatalf("friends read: %v", err)
	}
	if len(friends) != 2 {
		t.Fatalf("want 2 friends, got %v", friends)
	}
	if got := RefuseFriend("alice", friends); got != "ADMIT REFUSED bench=alice is a friend" {
		t.Errorf("a friend name refuses: %q", got)
	}
	if got := RefuseFriend("b2", friends); got != "" {
		t.Errorf("a non-friend name admits: %q", got)
	}
	// The bus saw no line: only the roster file exists.
	entries, err := os.ReadDir(busDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "participants.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the bus should see no new file, got %v", strings.Join(names, ","))
	}
}

// writeBenches writes a benches table: one header row then one tab-separated row per bench,
// the seven columns name host root cores harness auth wall.
func writeBenches(t *testing.T, dir string, rows ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("name\thost\troot\tcores\tharness\tauth\twall\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	path := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeBin writes a fake ssh and a fake rsync on a temp PATH: ssh records its full argv and
// exits 0 without running native, rsync records its argv and copies the source locally. The
// recorded argv lands in ssh.log and rsync.log under dir.
func fakeBin(t *testing.T, dir string) (sshLog, rsyncLog string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sshLog = filepath.Join(dir, "ssh.log")
	rsyncLog = filepath.Join(dir, "rsync.log")
	ssh := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + strconvQuote(sshLog) + "\n"
	rsync := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + strconvQuote(rsyncLog) + "\n" +
		"src=\"$1\"; dst=\"$2\"; dst=\"${dst#*:}\"\n" +
		"mkdir -p \"$(dirname \"$dst\")\"\n" +
		"cp \"$src\" \"$dst\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte(rsync), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return sshLog, rsyncLog
}

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

// coreIn returns the taskset core a run argv pins: the number that follows "taskset -c ".
func coreIn(run string) int {
	i := strings.Index(run, "taskset -c ")
	if i < 0 {
		return -1
	}
	rest := run[i+len("taskset -c "):]
	j := strings.IndexAny(rest, " \t")
	if j < 0 {
		j = len(rest)
	}
	n := 0
	for _, c := range rest[:j] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestBatchPinsSlotToCore: slot 3 on cores=1-15 runs under taskset -c 3, and every remote
// argv carries taskset. The fake ssh records each argv; none of them run native.
//
// The two cards run at once, so the order their argv lands in ssh.log is the order two
// concurrent slots happened to reach the fake -- not a fact about pinning. Reading runs[0]
// as card a's line made this test red 5 runs in 8 on this bench; the assertion is over the
// SET of run lines, each matched by its own label, which is what pinning actually claims.
func TestBatchPinsSlotToCore(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:3\tmodel\t"+a+"\nb\tb2:4\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	// The RUN argv lines, which are the ones that pin: an ssh that asks the bench a
	// question -- the pull's `test -f`, the idle watch's `stat` -- runs no card and pins
	// nothing. Selecting them by the command they carry is what keeps this assertion about
	// pinning rather than about how many other things a batch asks a bench.
	var runs []string
	for _, l := range readLines(t, sshLog) {
		if strings.Contains(l, "nova-swarm native") {
			runs = append(runs, l)
		}
	}
	if len(runs) == 0 {
		t.Fatalf("the fake ssh saw no run; the remote card never ran")
	}
	// The two remote cards' ssh writes land in ssh.log in whichever order the scheduler
	// ran them, so the RUN lines are not ordered by slot on return (#583). Sorting by the
	// pin they carry gives the loop below and any failure message a stable order.
	//
	// It is NOT what makes the slot->core assertions order-independent: sorting the lines
	// by the very core the test then names cannot tell card a on core 3 from card a on
	// core 4, so the pairing is asserted by matching each line's own --label instead.
	sort.Slice(runs, func(i, j int) bool {
		return coreIn(runs[i]) < coreIn(runs[j])
	})
	for _, l := range runs {
		if !strings.Contains(l, "taskset") {
			t.Fatalf("every remote run argv carries taskset, got %q", l)
		}
	}
	if len(runs) != 2 {
		t.Fatalf("two cards run, the fake ssh saw %d runs:\n%v", len(runs), runs)
	}
	pin := map[string]string{}
	for _, l := range runs {
		for _, label := range []string{"a", "b"} {
			if strings.Contains(l, "--label "+label+" ") {
				pin[label] = l
			}
		}
	}
	if !strings.Contains(pin["a"], "taskset -c 3") {
		t.Fatalf("card a on slot 3 of 1-15 runs under taskset -c 3, got %q", pin["a"])
	}
	if !strings.Contains(pin["b"], "taskset -c 4") {
		t.Fatalf("card b on slot 4 of 1-15 runs under taskset -c 4, got %q", pin["b"])
	}
}

// TestBatchRefusesMoreSlotsThanCores: 16 slots on 1-15 is ADMIT REFUSED bench=b2 slots=16
// cores=15 and no card starts.
func TestBatchRefusesMoreSlotsThanCores(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	var b strings.Builder
	for i := 0; i < 16; i++ {
		label := string(rune('a' + i))
		card := writeCard(t, dir, label+".card", "RESULT: "+label+"\nall green")
		b.WriteString(label + "\t-\tmodel\t" + card + "\n")
	}
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("16 slots on 15 cores is a refusal, got exit %d; stderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "ADMIT REFUSED bench=b2 slots=16 cores=15") {
		t.Fatalf("the refusal names the bench, slots and cores:\n%s", errb.String())
	}
	if lines := readLines(t, sshLog); len(lines) != 0 {
		t.Fatalf("no card may start on a refused bench, ssh saw %d runs", len(lines))
	}
}

// TestBatchCopiesCardOnly: exactly one file crosses before the run, and it is the card.
func TestBatchCopiesCardOnly(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	_, rsyncLog := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "B1", Deadline: 5 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	lines := readLines(t, rsyncLog)
	if len(lines) != 1 {
		t.Fatalf("exactly one file crosses before the run, rsync saw %d:\n%v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], a+" ") {
		t.Fatalf("the one file copied is the card: %q", lines[0])
	}
	if strings.Contains(lines[0], "runner") || strings.Contains(lines[0], "auth") || strings.Contains(lines[0], "config") {
		t.Fatalf("no runner script, config or key crosses: %q", lines[0])
	}
}
