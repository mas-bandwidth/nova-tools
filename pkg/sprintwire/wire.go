// Package sprintwire is how a worker talks to the sprint's server: the run
// loop on the coordinator's machine, the one writer of the sprint, which runs a
// worker's verbs for it, one at a time, beside the store. The server is single
// threaded and runs each request's batch in order, a plain client/server pair
// carries the verbs rather than a shared store, and both ends are Go. A request
// is the worker's verbs, each
// the argument list it would give nova-sprint, in the order to run them; the
// reply is each verb's exit code and what it printed. One request is one
// exchange whatever it carries, from beside the server or from 100 ms away.
package sprintwire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// Path is the server's one endpoint.
const Path = "/verbs"

// MaxRequest bounds a request's body, and MaxVerbs the verbs of one batch.
const (
	MaxRequest = 1 << 20
	MaxVerbs   = 256
)

// Request is a worker's verbs, in the order to run them.
type Request struct {
	Verbs [][]string `json:"verbs"`
}

// Result is one verb's answer: nova-sprint's exit code, and what it printed.
// Outcome tells the member what the verb did, apart from its exit code, which
// does not separate a definite refusal from an unknown outcome: "accepted"
// when the verb ran and its work is in the table, "refused" when it was
// refused (usage, or before any write), "unknown" when it is not known
// whether it happened (a cut, a store that did not confirm, or a clear that
// closed the step as it finished). Changed says whether the verb wrote:
// "yes", "no", or "unknown".
type Result struct {
	Code    int    `json:"code"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Outcome string `json:"outcome,omitempty"` // accepted | refused | unknown
	Changed string `json:"changed,omitempty"` // yes | no | unknown
}

// Response is the answers, one a verb, in the request's order.
type Response struct {
	Results []Result `json:"results"`
}

// The sprint answer is read through the one strict read (pkg/schemaread) and
// the answer table pkg/answerwire generates from schemas/answer (the home
// rule: the one definition of Response and VerbResult). The reader takes every
// form a text transport carries (S2): the binary wire, the padded standard
// base64 of it, and the schema JSON text, which is the legacy JSON the server
// writes today (json.Marshal of Response: the same keys, outcome and changed
// left out when empty, which the table reads as None). Damage is refused by
// name, a value past a bound is refused and never clamped, and every read is
// counted under its form (S4, the reader-first order C13): writers stay legacy.

// answerDoc is one read Response: the loaded row and the region that holds it.
// A row's pointer fields are self-relative offsets into the region, so the
// region lives as long as the row is converted.
type answerDoc struct {
	v      *answerwire.Response
	region []byte
}

// alignedRegion is a byte slice aligned for the generated table's loads.
func alignedRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// answerReportOf is the generated report as the strict read takes it. A key
// given twice in the JSON text is ambiguous: refused as malformed, never last
// wins. An unknown field is refused as malformed too: a damaged field id or key
// reads as an unknown field, and tolerated it would read the answer's results
// or a result's outcome as absent (the strict read's rule: damage is never read
// as absent). A newer server's field is met only after every reader is updated
// (S4: readers first, fleet-wide, then writers), so no current reader meets one.
func answerReportOf(r *answerwire.TableReport) schemaread.Report {
	return schemaread.Report{
		KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped),
		Malformed: r.Malformed || r.Duplicate > 0 || r.Unknown > 0, Reason: r.Reason,
	}
}

// answerCanonical says whether wire is the one image the generated writer writes
// of the answer it loaded. A damaged count or reference can load as another
// well-framed answer (an element count cut short of its body reads fewer
// results); the writer's own image of what was read differs from it, so such a
// wire is refused as malformed, never read as fewer or no results.
func answerCanonical(v *answerwire.Response, wire []byte) bool {
	n := answerwire.ResponseMeasure(v)
	if n != int64(len(wire)) {
		return false
	}
	buf := make([]byte, n)
	return answerwire.ResponseSave(v, buf) == n && bytes.Equal(buf, wire)
}

// answerCodec is the sprint answer as a schemaread.Codec. Its constraints are
// the rules schemas/answer/answer.schema leaves to the reader: at most
// MaxVerbs results (an unbounded array declares no count), and in each result
// an exit code, a stdout and a stderr, because an answer that names none of
// them is no answer, never exit 0.
var answerCodec = &schemaread.Codec[answerDoc]{
	Name: "Response", Help: "nova-sprint -h",
	Reset: func(d *answerDoc) { *d = answerDoc{} },
	Measure: func(wire []byte) error {
		_, err := answerwire.ResponseLoadMeasureReason(wire)
		return err
	},
	LoadWire: func(d *answerDoc, wire []byte) schemaread.Report {
		n := answerwire.ResponseLoadMeasure(wire)
		if n < 0 {
			return schemaread.Report{Malformed: true}
		}
		var r answerwire.TableReport
		d.region = alignedRegion(n)
		if d.v = answerwire.ResponseLoad(d.region, wire, &r); d.v == nil {
			return schemaread.Report{Malformed: true}
		}
		rep := answerReportOf(&r)
		if !rep.Malformed && rep.Reason == "" && !answerCanonical(d.v, wire) {
			rep.Malformed = true
		}
		return rep
	},
	LoadJSON: func(d *answerDoc, text []byte) schemaread.Report {
		var r answerwire.TableReport
		var b answerwire.ResponseBuilder
		if !b.Init() || !answerwire.ResponseFromJson(&b, text, &r) || !b.Lock() {
			return schemaread.Report{Malformed: true, Reason: r.Reason}
		}
		d.v, d.region = b.AsConst(), b.Region()
		return answerReportOf(&r)
	},
	Constraints: func(d *answerDoc, c *schemaread.Checker) {
		c.Required("results", d.v != nil)
		if d.v == nil {
			return
		}
		c.Bound("results", int64(d.v.Results.Count), 0, MaxVerbs)
		for i := range d.v.Results.Count {
			r := d.v.Results.At(i)
			if r == nil {
				c.Required("results", false)
				continue
			}
			c.Required("code", r.CodePresent)
			c.Required("stdout", r.Stdout != 0)
			c.Required("stderr", r.Stderr != 0)
			c.Enum("outcome", int(r.Outcome), int(answerwire.OutcomeCount)+1)
			c.Enum("changed", int(r.Changed), int(answerwire.ChangedCount)+1)
		}
	},
}

// answerReads is the one reader of the sprint answer, so its per-form counts
// are this process's reads of it (S4: the counter is the cutover's evidence).
var answerReads = schemaread.NewReader(answerCodec)

// answerText is a pointer field of a read row as the string it points at.
func answerText(slot *int64) string {
	v := answerwire.TableStringAt((*answerwire.TableRef)(slot))
	return string(v.Data[:v.Length])
}

// answerWord is an enum read as the word the legacy Result carries: empty for
// None (a server that names none), else the variant's own name.
func answerWord(none bool, name string) string {
	if none {
		return ""
	}
	return name
}

// readAnswer reads the sprint answer whole through rd, in any form the reader
// accepts, and converts it to the package's Result values. A malformed, clamped or damaged
// answer, or one with a result that names no exit code or output, is refused
// by name (`Response REFUSED: <reason>; run: nova-sprint -h`) and reads as no
// answer at all.
func readAnswer(rd *schemaread.Reader[answerDoc], raw []byte) ([]Result, error) {
	d, _, err := rd.Read(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, d.v.Results.Count)
	for i := range d.v.Results.Count {
		r := d.v.Results.At(i)
		out = append(out, Result{
			Code:    int(r.Code),
			Stdout:  answerText(&r.Stdout),
			Stderr:  answerText(&r.Stderr),
			Outcome: answerWord(r.Outcome == answerwire.OutcomeNone, answerwire.EnumNameOutcome(r.Outcome)),
			Changed: answerWord(r.Changed == answerwire.ChangedNone, answerwire.EnumNameChanged(r.Changed)),
		})
	}
	return out, nil
}

// Client sends verbs to a sprint server.
type Client struct {
	Addr string       // host:port
	HTTP *http.Client // nil is a client that waits Timeout for an answer
}

// Timeout is how long a client waits for the server's answer by default.
const Timeout = 2 * time.Minute

// Do sends the verbs in one request and returns their answers, one a verb. An
// error is a server that did not answer, or answered something else: nothing
// is known of what ran.
func (c Client) Do(ctx context.Context, verbs ...[]string) ([]Result, error) {
	body, err := json.Marshal(Request{Verbs: verbs})
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: Timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.Addr+Path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the sprint server at %s did not answer: %w", c.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the answer is read whole below, or not at all
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("the sprint server at %s: its answer was cut: %w", c.Addr, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the sprint server at %s refused the request (%s): %s", c.Addr, resp.Status, bytes.TrimSpace(raw))
	}
	// every result is read strictly, in any form the reader accepts (S2): an
	// answer that is refused, or a result with no exit code or output, is no
	// answer, never exit 0
	out, err := readAnswer(answerReads, raw)
	if err != nil {
		return nil, fmt.Errorf("the sprint server at %s: %w: nothing is known of what ran", c.Addr, err)
	}
	if len(out) != len(verbs) {
		return nil, fmt.Errorf("the sprint server at %s answered %d results for %d verbs", c.Addr, len(out), len(verbs))
	}
	return out, nil
}
