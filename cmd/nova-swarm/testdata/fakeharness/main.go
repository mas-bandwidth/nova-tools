// The FAKE HARNESS: a stand-in for a provider, on PATH, so the dispatcher is tested end to
// end with no provider, no network and no key that is worth anything.
//
// It reads the prompt file it is handed -- which is what a real harness does -- and obeys
// the FAKE- directives the test put in the task text. Everything it writes, it writes the
// way the prompt tells a worker to: a whole revision to RESULT.md.tmp, renamed over
// RESULT.md.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "fake harness: no prompt file")
		os.Exit(2)
	}
	// THE CONTRACT A REAL HARNESS HAS. `opencode run --model <m> -- <prompt>`: a
	// subcommand, the model, and the prompt FILE last. The fake refused nothing before
	// 2026-09-11, so the dispatcher's missing `--model` passed every test here and killed
	// two real jobs in two seconds. It refuses now, the way a real one does.
	if os.Getenv("FAKE_BACKGROUND_CHILD") != "1" {
		if err := checkInvocation(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "fake harness:", err)
			os.Exit(2)
		}
	}
	raw, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake harness: the prompt could not be read:", err)
		os.Exit(2)
	}
	prompt := string(raw)
	job := os.Getenv("NOVA_SWARM_JOB")
	jobDir = job
	data := os.Getenv("XDG_DATA_HOME")

	// The key reaches the child in its environment and nowhere else. The fake proves it is
	// there without printing it: a length, never a value.
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "FAKE_KEY=") {
			fmt.Println("fake harness: the key is present, length", len(env)-len("FAKE_KEY="))
		}
	}
	// The argv the dispatcher built, recorded for the test that proves the model reached
	// the child. It carries no task text -- the task is the prompt FILE -- so writing it
	// down puts nothing in a file that is not already in the process table.
	if job != "" {
		writeRecorded(filepath.Join(job, "argv"), []byte(strings.Join(os.Args[1:], " ")), 0o644)
	}
	// FAKE-LAUNCHES records one line per real invocation, before any directive can exit,
	// so a test can prove how many times the machinery retried a task.
	if _, ok := directive(prompt, "FAKE-LAUNCHES"); ok && job != "" {
		record(filepath.Join(job, "launches"))
		if f, err := os.OpenFile(filepath.Join(job, "launches"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, "launch")
			f.Close()
		}
	}

	// FAKE-PUBLISH-FIRST publishes the revision BEFORE the directives that spend, sleep or
	// get this worker killed. It is how a budget, an unverifiable source and a deadline are
	// tested against a worker that had already found something: rule 3's append-as-found
	// worker, seen from the machinery's side.
	published := false
	if _, ok := directive(prompt, "FAKE-PUBLISH-FIRST"); ok {
		findings, _ := number(prompt, "FAKE-FINDINGS")
		publish(job, prompt, findings, notesRead(job, prompt))
		published = true
	}

	if n, ok := number(prompt, "FAKE-REFUSE"); ok {
		for i := 0; i < n; i++ {
			fmt.Printf("fake harness: read of /etc/somewhere: permission denied (refused)\n")
		}
	}
	if arg, ok := directive(prompt, "FAKE-USAGE"); ok {
		writeUsage(data, arg)
	}
	// A provider 429 cannot ride the exit status: POSIX truncates 429 to 173. So the fake
	// prints the status the way a real harness does and exits non-zero, after its usage row
	// so `cost` has the numbers a rate-limited attempt did burn.
	if _, ok := directive(prompt, "FAKE-429"); ok {
		fmt.Println("fake harness: error: the provider answered HTTP 429 Too Many Requests (rate limit)")
		os.Exit(1)
	}
	if _, ok := directive(prompt, "FAKE-BADUSAGE"); ok {
		_ = os.MkdirAll(data, 0o755)
		writeRecorded(filepath.Join(data, "usage.tsv"), []byte("this file has no tab-separated header\x00"), 0o000)
	}
	if _, ok := directive(prompt, "FAKE-BACKGROUND"); ok {
		child := exec.Command(os.Args[0], "--background-child")
		child.Env = append(os.Environ(), "FAKE_BACKGROUND_CHILD=1")
		_ = child.Start()
		// The child outlives this process, which is exactly the violation rule 11 names.
	}
	// The other half of rule 11: a worker that forks and WAITS for its child leaves nothing
	// alive in its group, and is no violation at all.
	if _, ok := directive(prompt, "FAKE-FOREGROUND-CHILD"); ok && os.Getenv("FAKE_FOREGROUND_CHILD") != "1" {
		child := exec.Command(os.Args[0], "run", "--model", "none", "--", os.Args[len(os.Args)-1])
		child.Env = append(os.Environ(), "FAKE_FOREGROUND_CHILD=1")
		_ = child.Run()
	}
	if os.Getenv("FAKE_FOREGROUND_CHILD") == "1" {
		return
	}
	if os.Getenv("FAKE_BACKGROUND_CHILD") == "1" {
		time.Sleep(60 * time.Second)
		return
	}
	notes := notesRead(job, prompt)
	if n, ok := number(prompt, "FAKE-SLEEP"); ok {
		time.Sleep(time.Duration(n) * time.Second)
	}
	if _, ok := directive(prompt, "FAKE-NORESULT"); ok {
		os.Exit(0)
	}
	findings, _ := number(prompt, "FAKE-FINDINGS")
	if !published {
		publish(job, prompt, findings, notes)
	}
	if n, ok := number(prompt, "FAKE-RC"); ok {
		os.Exit(n)
	}
}

// notesRead is how many notes this worker read before it wrote its report.
func notesRead(job, prompt string) int {
	if _, ok := directive(prompt, "FAKE-NONOTES"); ok {
		return -1
	}
	notes := 0
	if job != "" {
		if body, err := os.ReadFile(filepath.Join(job, "note")); err == nil {
			trimmed := strings.TrimRight(string(body), "\n")
			if trimmed != "" {
				notes = strings.Count(trimmed, "\n") + 1
			}
		}
	}
	return notes
}

func publish(job, prompt string, findings, notes int) {
	var b strings.Builder
	b.WriteString("# a fake task\n\n")
	if _, plan := directive(prompt, "FAKE-NOHEAD"); !plan {
		b.WriteString("## Head\n")
		fmt.Fprintf(&b, "findings: %d\n", findings)
		if notes >= 0 {
			fmt.Fprintf(&b, "notes read: %d\n", notes)
		}
		b.WriteString("repo: mas-bandwidth/nova-tools\nrev: abc123\n")
		b.WriteString("What was asked, what the state is now, and the single most important fact.\n")
	} else {
		b.WriteString("## Plan\nI will read the files and then report.\n")
	}
	b.WriteString("\n## Findings\n")
	for i := 1; i <= findings; i++ {
		fmt.Fprintf(&b, "- finding %d: the count line prints on failure too `THE COUNT LINE PRINTS ON FAILURE AS WELL AS SUCCESS` SPEC.md:%d\n", i, 200+i)
	}
	if _, ok := directive(prompt, "FAKE-DUP"); ok {
		b.WriteString("- dup: a finding already owed `the owed list first` SPEC-SWARM.md:70\n")
	}
	// A complete review that found nothing, written the way a person writes it: the head
	// says `findings: 0` and the section says so in words. D3, 2026-09-11.
	if _, ok := directive(prompt, "FAKE-NONE-BULLET"); ok {
		b.WriteString("- none\n")
	}
	if _, ok := directive(prompt, "FAKE-UNQUOTED"); ok {
		b.WriteString("- a finding with no rule beside it and nothing to check it against\n")
	}
	b.WriteString("\n## Per item\n| item | state | evidence |\n| --- | --- | --- |\n")
	state, evidence := "green", "SPEC.md:1"
	if _, ok := directive(prompt, "FAKE-MALFORMED"); ok {
		state = "probably"
	}
	// A completed probe row that finished `not done` WITH ITS REASON (demanded test 8): a
	// worker that ran the item to the end and found it could not be decided is a finished
	// job, not a failed one, and the reason is the evidence cell.
	if _, ok := directive(prompt, "FAKE-NOTDONE"); ok {
		state, evidence = "not done", "the row could not be probed: the fixture it names is not in this tree"
	}
	fmt.Fprintf(&b, "| the item as it was handed to me | %s | %s |\n", state, evidence)
	b.WriteString("\n## Gates\n| name | result | seconds |\n| --- | --- | --- |\n| go test ./... | pass | 3 |\n")
	b.WriteString("\n## Left owed\n- nothing\n\n## One line\nA fake worker did a fake task.\n")

	tmp := filepath.Join(job, "RESULT.md.tmp")
	record(tmp)
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "fake harness: the report could not be written:", err)
		os.Exit(2)
	}
	if _, ok := directive(prompt, "FAKE-UNPUBLISHED"); ok {
		return
	}
	record(filepath.Join(job, "RESULT.md"))
	if err := os.Rename(tmp, filepath.Join(job, "RESULT.md")); err != nil {
		fmt.Fprintln(os.Stderr, "fake harness: the report could not be published:", err)
		os.Exit(2)
	}
}

func writeUsage(data, arg string) {
	_ = os.MkdirAll(data, 0o755)
	fields := strings.Fields(arg)
	values := []string{"-", "-", "-", "-", "-"}
	for i := 0; i < len(fields) && i < 5; i++ {
		values[i] = fields[i]
	}
	body := "tokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\tmodel\trepo\n" +
		strings.Join(values, "\t") + "\t0.0100\tfake-model\tmas-bandwidth/nova-tools\n"
	writeRecorded(filepath.Join(data, "usage.tsv"), []byte(body), 0o644)
}

// THE WRITE-PATH TRIPWIRE (demanded test 9, SPEC-SWARM.md:1264). Every path this child
// opens for writing is recorded in its OWN job directory, one per line, so a test can prove
// that no path was opened by two children rather than trust that slots keep them apart.
// The record file itself is the tripwire's own and is excluded by construction: it is the
// one path per child that the test never compares.
var jobDir string

func record(path string) {
	if jobDir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(jobDir, "writes"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, path)
}

func writeRecorded(path string, body []byte, perm os.FileMode) {
	record(path)
	_ = os.WriteFile(path, body, perm)
}

func directive(prompt, name string) (string, bool) {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, name) {
			return strings.TrimSpace(strings.TrimPrefix(line, name)), true
		}
	}
	return "", false
}

func number(prompt, name string) (int, bool) {
	arg, ok := directive(prompt, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.Fields(arg + " 0")[0])
	if err != nil {
		return 0, false
	}
	return n, true
}

// checkInvocation is what a real harness requires of its argv: its own subcommand, the
// model it is to run, and a readable prompt file last. A harness handed a bare path treats
// it as a project directory and does nothing at all.
func checkInvocation(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("the first argument wants a subcommand such as `run`, got %q", strings.Join(args, " "))
	}
	model := ""
	for i, a := range args {
		if a == "--model" && i+1 < len(args) {
			model = args[i+1]
		}
		if strings.HasPrefix(a, "--model=") {
			model = strings.TrimPrefix(a, "--model=")
		}
	}
	if model == "" {
		return fmt.Errorf("no --model in %q: this harness was never told which model to run", strings.Join(args, " "))
	}
	last := args[len(args)-1]
	if !strings.HasSuffix(last, "PROMPT.md") {
		return fmt.Errorf("the last argument wants the prompt FILE, got %q", last)
	}
	return nil
}
