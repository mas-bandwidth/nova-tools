package card

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

var (
	// ErrNoRecord means the results directory has no end.record file.
	ErrNoRecord = errors.New("no end record")
	// ErrBadRecord means a file named end.record is not an end record.
	ErrBadRecord = errors.New("bad end record")
)

// Result is one verb outcome. Code is the process exit: 0 applied, 1 usage,
// 2 a guard refused, 3 fenced, 4 conflict, 5 not found, 6 Redis unavailable.
// Resolved is true only when this call ended the attempt. A record that names
// another attempt is Code 0, Reason NOTHING, Resolved false, and no write.
type Result struct {
	Code     int
	Verb     string
	ID       string
	Attempt  int
	Receipt  string
	Reason   string
	Resolved bool
}

func (r Result) Line() string {
	if r.Resolved {
		return fmt.Sprintf("OK %s %s attempt=%d receipt=%s", r.Verb, r.ID, r.Attempt, r.Receipt)
	}
	reason := r.Reason
	if reason == "" {
		reason = "REFUSED"
	}
	return fmt.Sprintf("REFUSED %s %s %s", r.Verb, r.ID, reason)
}

// EndRecord is the file the wrapper writes before card end. The fields are
// the whole record. at is the wrapper's clock and is not a Redis time;
// ended_at is Redis TIME, read inside the function.
type EndRecord struct {
	Identity  Identity
	Outcome   string
	Reason    string
	ExitCode  int
	TokenSHA  string
	PushedSHA string
	At        string
}

func (rec EndRecord) validate() error {
	parsed, err := ParseIdentity(rec.Identity.String())
	if err != nil || parsed != rec.Identity {
		return ErrBadRecord
	}
	if !reasonOK(rec.Outcome, rec.Reason) || !validTokenSHA(rec.TokenSHA) || !validPushedSHA(rec.PushedSHA) || !validAt(rec.At) {
		return ErrBadRecord
	}
	return nil
}

func formatEndRecord(rec EndRecord) (string, error) {
	if err := rec.validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("identity %s\noutcome %s\nreason %s\nexit %d\ntoken_sha %s\npushed_sha %s\nat %s\n",
		rec.Identity.String(), rec.Outcome, rec.Reason, rec.ExitCode, rec.TokenSHA, rec.PushedSHA, rec.At), nil
}

func parseEndRecord(body string) (EndRecord, error) {
	lines := strings.Split(body, "\n")
	if len(lines) != 8 || lines[7] != "" {
		return EndRecord{}, ErrBadRecord
	}
	want := []string{"identity", "outcome", "reason", "exit", "token_sha", "pushed_sha", "at"}
	vals := make([]string, len(want))
	for i, key := range want {
		got, val, ok := strings.Cut(lines[i], " ")
		if !ok || got != key || val == "" || strings.ContainsAny(val, " \t") {
			return EndRecord{}, ErrBadRecord
		}
		vals[i] = val
	}
	exitCode, err := strconv.Atoi(vals[3])
	if err != nil || strconv.Itoa(exitCode) != vals[3] {
		return EndRecord{}, ErrBadRecord
	}
	id, err := ParseIdentity(vals[0])
	if err != nil {
		return EndRecord{}, ErrBadRecord
	}
	rec := EndRecord{
		Identity: id, Outcome: vals[1], Reason: vals[2], ExitCode: exitCode,
		TokenSHA: vals[4], PushedSHA: vals[5], At: vals[6],
	}
	if err := rec.validate(); err != nil {
		return EndRecord{}, ErrBadRecord
	}
	return rec, nil
}

func endRecordPath(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" || strings.ContainsAny(dir, "\r\n") {
		return "", fmt.Errorf("results dir is required")
	}
	return filepath.Join(filepath.Clean(dir), EndRecordName), nil
}

// ReadEndRecord reads the one end record in dir. A missing file is ErrNoRecord.
// Any other shape is ErrBadRecord and is not an end.
func ReadEndRecord(dir string) (EndRecord, error) {
	path, err := endRecordPath(dir)
	if err != nil {
		return EndRecord{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EndRecord{}, ErrNoRecord
		}
		return EndRecord{}, err
	}
	return parseEndRecord(string(body))
}

// WriteEndRecord writes the record the wrapper must leave before card end.
// It does not create dir: a results directory is named by the caller.
func WriteEndRecord(dir string, rec EndRecord) error {
	body, err := formatEndRecord(rec)
	if err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	path, err := endRecordPath(dir)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// EndRequest is card end from the wrapper. Outcome and reason must match the
// record; the record is what ends the card, and a mismatch writes nothing.
type EndRequest struct {
	Sprint     string
	Label      string
	Token      string
	Outcome    string
	Reason     string
	ResultsDir string
}

// ResolveRequest is the reconciler ingest of an end record already on disk.
// It does not take a token and it does not read a branch.
type ResolveRequest struct {
	Sprint     string
	Label      string
	ResultsDir string
}

// End refuses, and does not end the card, when the results directory has no
// end record for this attempt. That directory is
// <sprint>/<label>/<base sha8>/<bench>/<attempt>. A token that is not the
// attempt's token exits 3.
func End(ctx context.Context, st *store.Store, req EndRequest) (Result, error) {
	const verb = "card end"
	if st == nil || st.Client() == nil || !validSprintLabel(req.Sprint, req.Label) || req.Token == "" || req.Outcome == "" || req.Reason == "" || !absResults(req.ResultsDir) {
		return usage(verb, req.Label), nil
	}
	return callEnd(ctx, st, verb, "token", req.Sprint, req.Label, req.Token, req.ResultsDir, req.Outcome, req.Reason)
}

// Resolve ends the attempt only when its own directory holds an end record
// for this identity and its token_sha. A copy in another directory, another
// attempt of the same label, or another cut sha resolves nothing: the hash,
// the indexes, and the log stay as they were.
func Resolve(ctx context.Context, st *store.Store, req ResolveRequest) (Result, error) {
	const verb = "card resolve"
	if st == nil || st.Client() == nil || !validSprintLabel(req.Sprint, req.Label) || !absResults(req.ResultsDir) {
		return usage(verb, req.Label), nil
	}
	return callEnd(ctx, st, verb, "record", req.Sprint, req.Label, "", req.ResultsDir, "", "")
}

func callEnd(ctx context.Context, st *store.Store, verb, mode, sprint, label, token, results, claimOutcome, claimReason string) (Result, error) {
	dir, err := cleanResultsDir(results)
	if err != nil {
		return Result{}, err
	}
	// Read end.record only from this attempt's directory. A copy elsewhere
	// is not forwarded, and the function repeats the same bind before it writes.
	id, found, err := storedIdentity(ctx, st, sprint, label)
	if err != nil {
		if res, down := redisDown(verb, label, err); down {
			return res, nil
		}
		return Result{}, err
	}
	var rec EndRecord
	ok := false
	if found && resultsBound(dir, id) {
		rec, ok, err = loadRecord(dir)
		if err != nil {
			return Result{}, err
		}
	}
	ident, outcome, reason, sha, pushed, exitCode := "", "", "", "", "", ""
	if ok {
		ident = rec.Identity.String()
		outcome = rec.Outcome
		reason = rec.Reason
		sha = rec.TokenSHA
		pushed = rec.PushedSHA
		exitCode = strconv.Itoa(rec.ExitCode)
	}
	if mode == "record" && ok {
		claimOutcome = outcome
		claimReason = reason
	}
	reply, err := fcall(ctx, st, "ns_card_end", cardKeys(sprint, label),
		mode, sprint, label, token, dir,
		ident, outcome, reason, sha, pushed, exitCode,
		claimOutcome, claimReason)
	if err != nil {
		if res, down := redisDown(verb, label, err); down {
			return res, nil
		}
		return Result{}, err
	}
	return resultFrom(verb, label, reply), nil
}

// absResults reports whether dir can be the card hash field results: it is
// written once by ns_card_end and read by harvest with no root to join it to
// (#3329), so a relative dir is a usage error before anything is written.
func absResults(dir string) bool {
	return strings.TrimSpace(dir) != "" && filepath.IsAbs(dir)
}

func cleanResultsDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" || strings.ContainsAny(dir, "\r\n") {
		return "", fmt.Errorf("results dir is required")
	}
	return filepath.Clean(dir), nil
}

// storedIdentity is the attempt whose directory may hold end.record.
func storedIdentity(ctx context.Context, st *store.Store, sprint, label string) (Identity, bool, error) {
	raw, err := st.Client().HGet(ctx, CardKey(sprint, label), "identity").Result()
	if errors.Is(err, redis.Nil) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	id, err := ParseIdentity(raw)
	if err != nil {
		return Identity{}, false, nil
	}
	return id, true, nil
}

// resultsBound reports whether dir is the canonical attempt directory
// <sprint>/<label>/<base sha8>/<bench>/<attempt> under an absolute root.
func resultsBound(dir string, id Identity) bool {
	want := id.String()
	if want == "" || id.Attempt < 1 {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(dir))
	return strings.HasSuffix(clean, "/"+want)
}

func loadRecord(dir string) (EndRecord, bool, error) {
	rec, err := ReadEndRecord(dir)
	if err == nil {
		return rec, true, nil
	}
	if errors.Is(err, ErrNoRecord) || errors.Is(err, ErrBadRecord) {
		return EndRecord{}, false, nil
	}
	return EndRecord{}, false, err
}

func usage(verb, id string) Result {
	return Result{Code: 1, Verb: verb, ID: id, Reason: "USAGE"}
}

type fnReply struct {
	Code    int
	Status  string
	Attempt int
	Receipt string
}

func resultFrom(verb, id string, reply fnReply) Result {
	res := Result{
		Code: reply.Code, Verb: verb, ID: id, Attempt: reply.Attempt,
		Receipt: reply.Receipt, Reason: reply.Status,
		Resolved: reply.Code == 0 && reply.Status == "OK",
	}
	if res.Resolved {
		res.Reason = ""
	}
	return res
}

func cardKeys(sprint, label string) []string {
	return []string{CardKey(sprint, label), LogKey(sprint), IdemKey(sprint)}
}

func fcall(ctx context.Context, st *store.Store, name string, keys []string, args ...any) (fnReply, error) {
	if err := fn.Load(ctx, st.Client()); err != nil {
		return fnReply{}, err
	}
	raw, err := st.Client().FCall(ctx, name, keys, args...).Text()
	if err != nil {
		return fnReply{}, err
	}
	return parseReply(raw)
}

func parseReply(raw string) (fnReply, error) {
	parts := strings.Split(raw, "|")
	if len(parts) != 4 {
		return fnReply{}, fmt.Errorf("sprint function reply %q", raw)
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return fnReply{}, fmt.Errorf("sprint function reply %q", raw)
	}
	attempt := 0
	if parts[2] != "" {
		attempt, err = strconv.Atoi(parts[2])
		if err != nil {
			return fnReply{}, fmt.Errorf("sprint function reply %q", raw)
		}
	}
	return fnReply{Code: code, Status: parts[1], Attempt: attempt, Receipt: parts[3]}, nil
}

func redisDown(verb, id string, err error) (Result, bool) {
	if err == nil || !redisUnavailable(err) {
		return Result{}, false
	}
	return Result{Code: 6, Verb: verb, ID: id, Reason: "REDIS"}, true
}

func redisUnavailable(err error) bool {
	if errors.Is(err, redis.ErrClosed) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	msg := err.Error()
	for _, bit := range []string{"connection refused", "connect: ", "i/o timeout", "no such host", "EOF", "LOADING"} {
		if strings.Contains(msg, bit) {
			return true
		}
	}
	return false
}
