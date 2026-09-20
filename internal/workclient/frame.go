package workclient

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// The framed v1 wire BESIDE the S1 line wire above, never instead of it
// (docs/SPEC-WORK.md:2661-2693). The S1 wire carries one line in and one line
// out, which is enough for `session status` and enough for no listing verb:
// `query`, `operation list`, `savepoint list`, `verify` and the rest are built
// from the grammar's 25 ROW/NOTE/MORE shapes and answer many lines. The spec
// pins the replacement -- a versioned, bounded, length-prefixed UTF-8 JSON
// protocol whose response carries `lines` as an ordered ARRAY, an explicit
// `exit`, and the two revision fields -- and this file is the Go reader/writer
// for it, so the client half exists when the session attaches its own
// (docs/SPEC-WORK.md, "The wire is a versioned ... protocol").
//
// The rules the codec keeps, each one the spec's own:
//
//   - One message is a 4-byte big-endian unsigned length then that many bytes
//     of one UTF-8 JSON object. The length is read and bounded BEFORE the body
//     is allocated, so a session that names a huge frame costs the reader the
//     bound and not the allocation.
//   - Every integer the protocol carries is a JSON string of decimal digits,
//     and the protocol carries no JSON numbers at all. Decoding into Go strings
//     enforces it: a bare JSON number will not unmarshal into a string field
//     and refuses the frame instead of rounding it.
//   - Versions are negotiated before any request. The first frame is
//     `{"op":"hello","protocol":["1"],"client":"<build identity>"}` and the
//     session answers `hello-ok` with the one version it will speak, or
//     `hello-refused` naming what it supports. An unsupported version is
//     refused clearly and never degrades into a guess.
//   - Every ordinary response echoes its request id and is matched by it, never
//     by arrival order. A response for another id is a protocol error.
//   - The whole response is validated before any line is handed back, so a
//     malformed answer prints nothing.

// DefaultMaxFrameBytes bounds one v1 frame on the client side. The spec
// defaults --max-frame-bytes to the session's --max-bytes; a thin client that
// has not been told the session's number uses its own standing bound, named
// here so one line replaces it.
const DefaultMaxFrameBytes = 1 << 20

// The ways one framed exchange can fail to become a response. They are
// separate sentinels because they send a reader to different places: a version
// is a session speaking a protocol this client does not have, a malformed frame
// is a broken peer, and a correlation miss is a reply to somebody else.
var (
	// ErrFrameVersion: the session would not speak protocol v1.
	ErrFrameVersion = errors.New("the session does not speak protocol 1")
	// ErrFrameMalformed: the frame is not one v1 JSON object, or an integer
	// arrived as a JSON number.
	ErrFrameMalformed = errors.New("the session's frame is not a v1 object")
	// ErrFrameCorrelation: the response does not echo the request id.
	ErrFrameCorrelation = errors.New("the session's frame does not echo the request id")
	// ErrFrameTooLong: a frame's declared length is past the bound, refused
	// before its body is read or allocated.
	ErrFrameTooLong = errors.New("the session's frame is past the bound")
)

// Frame is one v1 request: the typed operation name the spec pins (`op` a typed
// operation name and never an executable form), its client-drawn request id,
// its author, and the optional expect/now/max/deadline fields and args. Every
// top-level value travels as the caller spelled it, a string, because the
// session validates and a thin client never second-guesses the engine's bounds.
// Args values are the caller's own JSON-safe spellings -- strings, arrays or
// booleans, never a JSON number, which the protocol forbids and the session
// refuses -- so the writer never emits the number the contract rules out.
type Frame struct {
	Op       string
	Request  string
	As       string
	Expect   string
	Now      string
	Max      string
	Deadline string
	Args     map[string]any
}

// Reply is one decoded v1 response: the ordered `lines` the output grammar
// spells, the explicit exit code the one-line wire has no field for, and the
// two revision fields the spec keeps in every response.
type Reply struct {
	Request string
	OK      bool
	Exit    string
	Lines   []string
	Rev     string
	Pushed  string
}

// ClientIdentity is the `client` field of the hello offer. It is a variable so
// the binary can stamp the build identity without this package reading main's
// flags; the zero value is the honest floor the rest of the repo uses.
var ClientIdentity = "devel"

// ExchangeFramed negotiates protocol v1 over SOCKET, sends one request frame
// and answers the response's ordered lines, its exit and its revisions, under
// the package's standing wall-clock bound.
func ExchangeFramed(socket string, frame Frame) (Reply, error) {
	return ExchangeFramedWithin(socket, frame, DefaultTimeout)
}

// ExchangeFramedWithin is ExchangeFramed under a caller's own bound, spent
// across the dial, the hello, the write and the read as one budget. A bound of
// zero or less sets no deadline at all, the one way back to waiting forever.
//
// A failed hello closes the connection and answers ErrFrameVersion: the S1 line
// wire is a separate, explicitly selected wire and this function never silently
// downgrades to it.
func ExchangeFramedWithin(socket string, frame Frame, within time.Duration) (Reply, error) {
	deadline := budget(within)
	conn, err := dial(socket, deadline)
	if err != nil {
		return Reply{}, classify(err, within)
	}
	defer conn.Close()
	if !deadline.IsZero() {
		if err := conn.SetDeadline(deadline); err != nil {
			return Reply{}, err
		}
	}
	r := bufio.NewReader(conn)
	if err := negotiate(r, conn); err != nil {
		return Reply{}, err
	}
	body, err := requestBody(frame)
	if err != nil {
		return Reply{}, err
	}
	if err := writeFrame(conn, body); err != nil {
		return Reply{}, classify(err, within)
	}
	respBody, err := readFrame(r, DefaultMaxFrameBytes)
	if err != nil {
		return Reply{}, classify(err, within)
	}
	return decodeReply(respBody, frame.Request)
}

// helloOffer is the first client frame: the versions this client speaks and the
// build identity it is.
type helloOffer struct {
	Op       string   `json:"op"`
	Protocol []string `json:"protocol"`
	Client   string   `json:"client"`
}

// helloAnswer is either `hello-ok` (the one version the session will speak) or
// `hello-refused` (the versions it does support and why).
type helloAnswer struct {
	Op        string   `json:"op"`
	Protocol  string   `json:"protocol"`
	Session   string   `json:"session"`
	Supported []string `json:"supported"`
	Reason    string   `json:"reason"`
}

// negotiate performs the one exchange without a request id: it offers protocol
// v1 and requires `hello-ok` for v1. Anything else -- a refusal, another
// protocol, a frame that is not a hello answer -- is refused and the
// connection is left to close, never downgraded.
func negotiate(r *bufio.Reader, w io.Writer) error {
	offer, err := json.Marshal(helloOffer{Op: "hello", Protocol: []string{"1"}, Client: ClientIdentity})
	if err != nil {
		return err
	}
	if err := writeFrame(w, offer); err != nil {
		return err
	}
	body, err := readFrame(r, DefaultMaxFrameBytes)
	if err != nil {
		return err
	}
	var answer helloAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("%w: hello answer: %v", ErrFrameMalformed, err)
	}
	switch answer.Op {
	case "hello-ok":
		if answer.Protocol != "1" {
			return fmt.Errorf("%w: session picked %q", ErrFrameVersion, answer.Protocol)
		}
		return nil
	case "hello-refused":
		return fmt.Errorf("%w: session supports %v: %s", ErrFrameVersion, answer.Supported, answer.Reason)
	default:
		return fmt.Errorf("%w: hello answer carries op=%q", ErrFrameMalformed, answer.Op)
	}
}

// requestBody encodes one request frame. The four optional fields are omitted
// when empty -- the spec's absent key means *not given* -- and args is always
// an object, empty when there are none.
func requestBody(frame Frame) ([]byte, error) {
	body := map[string]any{
		"op":      frame.Op,
		"request": frame.Request,
		"as":      frame.As,
		"args":    argsOrEmpty(frame.Args),
	}
	for key, value := range map[string]string{
		"expect":   frame.Expect,
		"now":      frame.Now,
		"max":      frame.Max,
		"deadline": frame.Deadline,
	} {
		if value != "" {
			body[key] = value
		}
	}
	return json.Marshal(body)
}

func argsOrEmpty(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

// rawReply is the response envelope as it arrives, every field a pointer so an
// absent key is told from a zero value before anything is handed back.
type rawReply struct {
	Request *string   `json:"request"`
	OK      *bool     `json:"ok"`
	Exit    *string   `json:"exit"`
	Lines   *[]string `json:"lines"`
	Rev     *string   `json:"rev"`
	Pushed  *string   `json:"pushed"`
}

// decodeReply validates the whole response before any line is returned: every
// field the spec requires is present, the response echoes the request id, and
// exit is one of the three codes. A missing field, a foreign id or an
// out-of-range exit refuses the frame rather than printing part of it.
func decodeReply(body []byte, want string) (Reply, error) {
	var raw rawReply
	if err := json.Unmarshal(body, &raw); err != nil {
		return Reply{}, fmt.Errorf("%w: %v", ErrFrameMalformed, err)
	}
	if raw.Request == nil || raw.OK == nil || raw.Exit == nil ||
		raw.Lines == nil || raw.Rev == nil || raw.Pushed == nil {
		return Reply{}, fmt.Errorf("%w: response is missing a required field", ErrFrameMalformed)
	}
	if *raw.Request != want {
		return Reply{}, fmt.Errorf("%w: got %q want %q", ErrFrameCorrelation, *raw.Request, want)
	}
	switch *raw.Exit {
	case "0", "1", "2":
	default:
		return Reply{}, fmt.Errorf("%w: exit=%q", ErrFrameMalformed, *raw.Exit)
	}
	return Reply{
		Request: *raw.Request,
		OK:      *raw.OK,
		Exit:    *raw.Exit,
		Lines:   *raw.Lines,
		Rev:     *raw.Rev,
		Pushed:  *raw.Pushed,
	}, nil
}

// writeFrame writes payload as one v1 message: a 4-byte big-endian unsigned
// length and the bytes it names.
func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > int(^uint32(0)) {
		return fmt.Errorf("%w: %d bytes", ErrFrameTooLong, len(payload))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one 4-byte length prefix and the body it names, bounding the
// declared length before the body is allocated. A prefix past max is refused
// whole and one byte of the body is never read.
func readFrame(r io.Reader, max int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if int64(n) > int64(max) {
		return nil, fmt.Errorf("%w: %d bytes past the %d-byte bound", ErrFrameTooLong, n, max)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
