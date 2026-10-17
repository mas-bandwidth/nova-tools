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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
	"unsafe"

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

// responseDoc is one ResponseWire read: the loaded row and the region that
// holds it. The generated table's pointer paths are self-relative offsets into
// the region, so the region lives as long as the read is converted.
type responseDoc struct {
	v      *ResponseWire
	region []byte
}

// alignedRegion is a byte slice aligned for the generated table's loads.
func alignedRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func responseReportOf(r *TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped),
		Malformed: r.Malformed || r.Duplicate > 0, Reason: r.Reason,
	}
}

// responseCodec is the sprint answer's read as a schemaread.Codec (S2): it
// reads the binary wire, its padded standard base64, the schema JSON text and
// the legacy JSON the server writes today, and counts each read under its form.
var responseCodec = &schemaread.Codec[responseDoc]{
	Name: "Response", Help: "nova-sprint -h",
	Reset: func(d *responseDoc) { *d = responseDoc{} },
	Measure: func(wire []byte) error {
		_, err := ResponseWireLoadMeasureReason(wire)
		return err
	},
	LoadWire: func(d *responseDoc, wire []byte) schemaread.Report {
		n := ResponseWireLoadMeasure(wire)
		if n < 0 {
			return schemaread.Report{Malformed: true}
		}
		var r TableReport
		d.region = alignedRegion(n)
		if d.v = ResponseWireLoad(d.region, wire, &r); d.v == nil {
			return schemaread.Report{Malformed: true}
		}
		return responseReportOf(&r)
	},
	LoadJSON: func(d *responseDoc, text []byte) schemaread.Report {
		var r TableReport
		var b ResponseWireBuilder
		if !b.Init() || !ResponseWireFromJson(&b, text, &r) || !b.Lock() {
			return schemaread.Report{Malformed: true, Reason: r.Reason}
		}
		d.v, d.region = b.AsConst(), b.Region()
		return responseReportOf(&r)
	},
	Constraints: func(d *responseDoc, c *schemaread.Checker) {
		c.Required("results", d.v != nil)
		if d.v == nil {
			return
		}
		c.Bound("results", int64(d.v.Results.Count), 0, MaxVerbs)
		for i := int32(0); i < d.v.Results.Count; i++ {
			if r := d.v.Results.At(i); r != nil {
				c.Required("code", true)
				c.Required("stdout", r.StdoutLength >= 0)
				c.Required("stderr", r.StderrLength >= 0)
			}
		}
	},
	SaveWire: func(d *responseDoc) []byte {
		n := ResponseWireMeasure(d.v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if ResponseWireSave(d.v, buf) != n {
			return nil
		}
		return buf
	},
	SaveJSON: func(d *responseDoc) []byte {
		n := ResponseWireToJsonMeasure(d.v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if ResponseWireToJson(d.v, buf) != n {
			return nil
		}
		return buf
	},
}

func responseReader() *schemaread.Reader[responseDoc] {
	return schemaread.NewReader(responseCodec)
}

// readResponse reads the sprint answer whole, in any of the forms the table
// reader accepts (the binary wire and its padded standard base64), and
// converts it to the package's Result values. A malformed, clamped or damaged
// image is refused by name and reads as no answer.
func readResponse(raw []byte) ([]Result, error) {
	d, _, err := responseReader().Read(raw)
	if err != nil {
		return nil, err
	}
	if d.v == nil {
		return nil, fmt.Errorf("Response REFUSED: absent_required (results); run: nova-sprint -h")
	}
	out := make([]Result, 0, d.v.Results.Count)
	for i := int32(0); i < d.v.Results.Count; i++ {
		r := d.v.Results.At(i)
		if r == nil {
			return nil, fmt.Errorf("Response REFUSED: malformed (results); run: nova-sprint -h")
		}
		out = append(out, Result{
			Code:    int(r.Code),
			Stdout:  string(r.Stdout[:r.StdoutLength]),
			Stderr:  string(r.Stderr[:r.StderrLength]),
			Outcome: string(r.Outcome[:r.OutcomeLength]),
			Changed: string(r.Changed[:r.ChangedLength]),
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
	// the answer is read in any accepted form (S2): the binary wire and its
	// padded standard base64 through the table reader; the JSON text and the
	// legacy text the server writes today through the legacy strict reader,
	// because the generated ResultWire row cannot tell an omitted required field
	// from an empty one
	t := bytes.TrimLeft(raw, " \t\r\n")
	useTable := len(t) > 0 && (t[0] < 0x20 || t[0] >= 0x80)
	if !useTable && len(t) > 0 {
		if t[0] >= 'A' && t[0] <= 'Z' || t[0] >= 'a' && t[0] <= 'z' || t[0] >= '0' && t[0] <= '9' || t[0] == '+' || t[0] == '/' {
			_, decErr := base64.StdEncoding.DecodeString(string(t))
			useTable = decErr == nil
		}
	}
	if useTable {
		out, err := readResponse(raw)
		if err != nil {
			return nil, fmt.Errorf("the sprint server at %s: %w", c.Addr, err)
		}
		if len(out) != len(verbs) {
			return nil, fmt.Errorf("the sprint server at %s answered %d results for %d verbs", c.Addr, len(out), len(verbs))
		}
		return out, nil
	}
	{
		var got struct {
			Results []*struct {
				Code    *int    `json:"code"`
				Stdout  *string `json:"stdout"`
				Stderr  *string `json:"stderr"`
				Outcome string  `json:"outcome"`
				Changed string  `json:"changed"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &got); err != nil || len(got.Results) != len(verbs) {
			return nil, fmt.Errorf("the sprint server at %s answered %d results for %d verbs", c.Addr, len(got.Results), len(verbs))
		}
		out := make([]Result, len(got.Results))
		for i, r := range got.Results {
			if r == nil || r.Code == nil || r.Stdout == nil || r.Stderr == nil {
				return nil, fmt.Errorf("the sprint server at %s: result %d of %d names no exit code or output: nothing is known of what ran", c.Addr, i+1, len(verbs))
			}
			out[i] = Result{Code: *r.Code, Stdout: *r.Stdout, Stderr: *r.Stderr, Outcome: r.Outcome, Changed: r.Changed}
		}
		return out, nil
	}
}
