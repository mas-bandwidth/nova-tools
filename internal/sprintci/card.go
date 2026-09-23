package sprintci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrCutExists means this head's card is already in the front tier. Cut does
// not write a second file and does not change the first one.
var ErrCutExists = errors.New("ci card already in the front tier")

// CutInput is one ci cut. Front is the front tier directory. Mirror is the
// absolute path of the bench mirror, never a GitHub URL. Paths are the
// packages the script passes to go test.
type CutInput struct {
	Repo   string
	PR     int
	SHA    string
	Paths  []string
	Mirror string
	Front  string
}

// CutCard is a script card read back from the front tier.
type CutCard struct {
	Card   Card
	Paths  []string
	Mirror string
	Script string
	Body   string
}

// Bench is one machine running a dealt CI card. Root is its job directory.
// Redis is the address the card's end writes.
type Bench struct {
	Name   string
	Dealer *Dealer
	Redis  string
	Root   string
}

// Result is what a bench run did. Started is false when the run did not
// start, which includes a bench with no dealt slot. Value is the Redis
// record when the end wrote one.
type Result struct {
	Started bool
	Reason  string
	Key     string
	Value   string
}

// Cut writes ci-<pr>-<sha8>.md into the front tier. The card is KIND script
// and names zero model calls. A second cut of the same head returns
// ErrCutExists and the path of the card already there.
func Cut(in CutInput) (string, error) {
	body, name, err := render(in)
	if err != nil {
		return "", err
	}
	if in.Front == "" || !filepath.IsAbs(in.Front) {
		return "", fmt.Errorf("front tier wants an absolute directory")
	}
	if err := os.MkdirAll(in.Front, 0o755); err != nil {
		return "", fmt.Errorf("front tier: %w", err)
	}
	path := filepath.Join(in.Front, name)
	if _, err := os.Stat(path); err == nil {
		return path, ErrCutExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("front tier: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("front tier: %w", err)
	}
	return path, nil
}

// Read reads a card Cut wrote. It refuses a card that is not KIND script or
// that does not say zero model calls, so a model card is not run as CI.
func Read(path string) (CutCard, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return CutCard{}, fmt.Errorf("card: %w", err)
	}
	return parseCard(string(raw))
}

// VerdictKey is the Redis key ci:<repo>:<sha> for this exact head. It is
// the card id. PR 1 is only so the head can be checked: the pull request
// number is not part of the key.
func VerdictKey(repo, sha string) (string, error) {
	return Card{Repo: repo, PR: 1, SHA: sha}.ID()
}

// Run starts the card's script on this bench only when the dealer has dealt
// the card's slot here. Otherwise it does not start: no checkout, no script,
// no Redis write. A start checks the mirror out of the bench mirror at the
// sha and then the card's end writes the verdict.
func (b *Bench) Run(ctx context.Context, cardPath string) (Result, error) {
	if b == nil || b.Dealer == nil {
		return Result{Reason: "no dealer"}, nil
	}
	if strings.TrimSpace(b.Name) == "" {
		return Result{Reason: NoDealtSlot}, nil
	}
	cut, err := Read(cardPath)
	if err != nil {
		return Result{}, err
	}
	ok, reason := b.Dealer.Start(b.Name, cut.Card)
	if !ok {
		return Result{Started: false, Reason: reason}, nil
	}
	if err := checkMirror(cut.Mirror); err != nil {
		return Result{Started: false, Reason: err.Error()}, nil
	}
	if strings.TrimSpace(b.Redis) == "" {
		return Result{}, fmt.Errorf("bench redis address is required")
	}
	if b.Root == "" || !filepath.IsAbs(b.Root) {
		return Result{}, fmt.Errorf("bench root wants an absolute directory")
	}
	id, err := cut.Card.ID()
	if err != nil {
		return Result{}, err
	}
	job := filepath.Join(b.Root, jobSegment(id), strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(filepath.Join(job, "tmp"), 0o755); err != nil {
		return Result{}, err
	}
	scriptPath := filepath.Join(job, "run.sh")
	if err := os.WriteFile(scriptPath, []byte(cut.Script), 0o700); err != nil {
		return Result{}, err
	}
	work := filepath.Join(job, "work")
	result := filepath.Join(job, "RESULT.md")
	cmd := exec.CommandContext(ctx, "/bin/sh", scriptPath)
	cmd.Env = benchEnv(cut, work, result, job)
	out, runErr := cmd.CombinedOutput()
	if _, statErr := os.Stat(result); statErr != nil {
		if runErr != nil {
			return Result{Started: true}, fmt.Errorf("script wrote no RESULT.md: %v: %s", runErr, tail(out))
		}
		return Result{Started: true}, fmt.Errorf("script wrote no RESULT.md: %s", tail(out))
	}
	key, value, err := writeVerdict(ctx, b.Redis, cut.Card.Repo, cut.Card.SHA, result)
	if err != nil {
		return Result{Started: true}, err
	}
	return Result{Started: true, Key: key, Value: value}, nil
}

func render(in CutInput) (body, name string, err error) {
	c := Card{Repo: in.Repo, PR: in.PR, SHA: in.SHA}
	sha, err := c.norm()
	if err != nil {
		return "", "", err
	}
	c.SHA = sha
	id, err := c.ID()
	if err != nil {
		return "", "", err
	}
	name, err = c.fileName()
	if err != nil {
		return "", "", err
	}
	paths, err := cleanPaths(in.Paths)
	if err != nil {
		return "", "", err
	}
	if err := checkMirror(in.Mirror); err != nil {
		return "", "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: %s sha=%s\n", id, sha[:12])
	b.WriteString("KIND: script\n")
	fmt.Fprintf(&b, "PATHS: %s\n", strings.Join(paths, ", "))
	b.WriteString("TEST: none\n")
	b.WriteString("MODEL CALLS: 0\n")
	fmt.Fprintf(&b, "REPO: %s\n", c.Repo)
	fmt.Fprintf(&b, "PR: %d\n", c.PR)
	fmt.Fprintf(&b, "SHA: %s\n", sha)
	fmt.Fprintf(&b, "MIRROR: %s\n", in.Mirror)
	b.WriteString("\n")
	b.WriteString("This card is a script. It makes zero model calls.\n")
	b.WriteString("The bench checks out the mirror at the sha and does not clone from GitHub.\n\n")
	b.WriteString("```sh\n")
	b.WriteString(benchScript)
	b.WriteString("```\n")
	return b.String(), name, nil
}

// jobSegment is the card id as one directory under the bench root. The id
// is ci:<repo>:<sha>, and a repository name contains a slash. That slash is
// not a path separator here, or the job can leave Root.
func jobSegment(id string) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(id)
}

func parseCard(body string) (CutCard, error) {
	header, _, ok := strings.Cut(body, "\n\n")
	if !ok {
		return CutCard{}, fmt.Errorf("card has no header")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		key, val, ok := splitHeader(line)
		if !ok {
			return CutCard{}, fmt.Errorf("card header line %q is not KEY: value", line)
		}
		if _, dup := fields[key]; dup {
			return CutCard{}, fmt.Errorf("card header repeats %s", key)
		}
		fields[key] = val
	}
	if fields["KIND"] != "script" {
		return CutCard{}, fmt.Errorf("KIND wants script, got %q", fields["KIND"])
	}
	if fields["MODEL CALLS"] != "0" {
		return CutCard{}, fmt.Errorf("a ci card makes zero model calls, got MODEL CALLS: %q", fields["MODEL CALLS"])
	}
	pr, err := strconv.Atoi(fields["PR"])
	if err != nil {
		return CutCard{}, fmt.Errorf("PR wants a pull request number, got %q", fields["PR"])
	}
	c := Card{Repo: fields["REPO"], PR: pr, SHA: fields["SHA"]}
	sha, err := c.norm()
	if err != nil {
		return CutCard{}, err
	}
	c.SHA = sha
	paths, err := cleanPaths(splitList(fields["PATHS"]))
	if err != nil {
		return CutCard{}, err
	}
	if err := checkMirror(fields["MIRROR"]); err != nil {
		return CutCard{}, err
	}
	script, err := scriptOf(body)
	if err != nil {
		return CutCard{}, err
	}
	return CutCard{Card: c, Paths: paths, Mirror: fields["MIRROR"], Script: script, Body: body}, nil
}

func scriptOf(body string) (string, error) {
	const open = "```sh\n"
	start := strings.Index(body, open)
	if start < 0 {
		return "", fmt.Errorf("card has no script")
	}
	rest := body[start+len(open):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return "", fmt.Errorf("card script is not closed")
	}
	script := rest[:end]
	if strings.TrimSpace(script) == "" {
		return "", fmt.Errorf("card script is empty")
	}
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	return script, nil
}

func splitHeader(line string) (key, val string, ok bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	val = strings.TrimSpace(line[i+1:])
	if key == "" || (strings.ContainsAny(key, " \t") && key != "MODEL CALLS") {
		return "", "", false
	}
	return key, val, true
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("PATHS wants the packages this pass tests")
	}
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || strings.ContainsAny(p, " \t\r\n,") {
			return nil, fmt.Errorf("a PATHS package is one token, got %q", p)
		}
		if strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.Contains(p, "://") {
			return nil, fmt.Errorf("PATHS package %q is not a repository-relative package", p)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

func checkMirror(mirror string) error {
	if mirror == "" || !filepath.IsAbs(mirror) {
		return fmt.Errorf("mirror wants the absolute path of the bench mirror")
	}
	lower := strings.ToLower(mirror)
	if strings.Contains(mirror, "://") || strings.Contains(lower, "github.com") {
		return fmt.Errorf("refusing a clone from GitHub; the bench checks out the mirror")
	}
	return nil
}

func writeVerdict(ctx context.Context, addr, repo, sha, resultPath string) (key, value string, err error) {
	if strings.TrimSpace(addr) == "" {
		return "", "", fmt.Errorf("redis address is required")
	}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return "", "", fmt.Errorf("RESULT.md: %w", err)
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	line = strings.TrimRight(line, "\r")
	value, err = parseResult(line)
	if err != nil {
		return "", "", err
	}
	key, err = VerdictKey(repo, sha)
	if err != nil {
		return "", "", err
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	defer rdb.Close()
	if err := rdb.Set(ctx, key, value, 0).Err(); err != nil {
		return "", "", fmt.Errorf("redis %s: %w", key, err)
	}
	return key, value, nil
}

func parseResult(line string) (string, error) {
	const prefix = "RESULT: "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("RESULT.md first line wants RESULT: OK or RESULT: FAIL <pkg> <test>")
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if rest == "OK" {
		return "OK", nil
	}
	parts := strings.Fields(rest)
	if len(parts) != 3 || parts[0] != "FAIL" || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("RESULT.md first line wants RESULT: FAIL <pkg> <test>, got %q", line)
	}
	return rest, nil
}

func benchEnv(cut CutCard, work, result, job string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + filepath.Join(job, "tmp"),
		"MIRROR=" + cut.Mirror,
		"SHA=" + cut.Card.SHA,
		"WORK=" + work,
		"RESULT=" + result,
		"PATHS=" + strings.Join(cut.Paths, ","),
		"GOMAXPROCS=8",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GO111MODULE=on",
		"CGO_ENABLED=0",
		"GOTELEMETRY=off",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
}

func tail(b []byte) string {
	s := string(b)
	if len(s) > 800 {
		s = s[len(s)-800:]
	}
	return strings.TrimSpace(s)
}

// benchScript is the card body. It checks out MIRROR at SHA into WORK and
// runs gofmt, go vet, and go test on PATHS. A URL is not a mirror: the
// script refuses before git runs, so it cannot clone from GitHub.
const benchScript = `set -eu

case "${MIRROR}" in
  *://*|*github.com*|*@github.com:*)
    printf 'RESULT: FAIL mirror clone\n' > "${RESULT}"
    exit 2
    ;;
esac

case "${WORK}" in
  /*/*) ;;
  *)
    printf 'RESULT: FAIL work path\n' > "${RESULT}"
    exit 2
    ;;
esac

mkdir -p "${WORK}"
if ! git clone --quiet -- "${MIRROR}" "${WORK}/src"; then
  printf 'RESULT: FAIL checkout mirror\n' > "${RESULT}"
  exit 1
fi
if ! git -C "${WORK}/src" checkout --quiet --detach "${SHA}"; then
  printf 'RESULT: FAIL checkout sha\n' > "${RESULT}"
  exit 1
fi
cd "${WORK}/src"

old_ifs=${IFS}
IFS=','
set --
for raw in ${PATHS}; do
  IFS=${old_ifs}
  p=$(printf '%s' "${raw}" | tr -d '[:space:]')
  if [ -n "${p}" ]; then
    set -- "$@" "${p}"
  fi
  IFS=','
done
IFS=${old_ifs}

if [ "$#" -eq 0 ]; then
  printf 'RESULT: FAIL paths empty\n' > "${RESULT}"
  exit 2
fi

export GOMAXPROCS=8

fmt_out=$(gofmt -l "$@" || true)
if [ -n "${fmt_out}" ]; then
  first=$(printf '%s\n' "${fmt_out}" | awk 'NR == 1 { print; exit }')
  printf 'RESULT: FAIL %s gofmt\n' "${first}" > "${RESULT}"
  exit 1
fi

if ! go vet "$@"; then
  printf 'RESULT: FAIL %s vet\n' "$1" > "${RESULT}"
  exit 1
fi

log=${WORK}/test.log
set +e
go test -p 4 "$@" >"${log}" 2>&1
code=$?
set -e
if [ "${code}" -ne 0 ]; then
  pkg=$(awk '/^FAIL[[:space:]]+[^[:space:]]/ { print $2; exit }' "${log}")
  name=$(awk '/^--- FAIL: / { print $3; exit }' "${log}")
  if [ -z "${pkg}" ]; then
    pkg=$1
  fi
  if [ -z "${name}" ]; then
    name=test
  fi
  printf 'RESULT: FAIL %s %s\n' "${pkg}" "${name}" > "${RESULT}"
  exit 1
fi
printf 'RESULT: OK\n' > "${RESULT}"
`
