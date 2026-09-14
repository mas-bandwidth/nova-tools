package review

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

const testHead = "0123456789abcdef0123456789abcdef01234567"

var testSubmission = merge.Submission{At: "2026-09-14T01:02:03Z", Rand: "a1b2c3"}

func TestAnswerItemGoldenRoundTrip(t *testing.T) {
	item, err := AnswerItem("951/feature", "rowan", "20260913T140102Z-a1b2c3.1", "dup", "20260913T140102Z-a1b2c3.0", testHead, "same root cause", testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	const wantPath = "reviews/951%2Ffeature/answer-rowan-0123456789ab-20260914T010203Z-a1b2c3.json"
	const wantBody = `{
  "version": 1,
  "entry": "951/feature",
  "who": "rowan",
  "finding": "20260913T140102Z-a1b2c3.1",
  "as": "dup",
  "of": "20260913T140102Z-a1b2c3.0",
  "head": "0123456789abcdef0123456789abcdef01234567",
  "at": "2026-09-14T01:02:03Z",
  "note": "same root cause",
  "file": "reviews/951%2Ffeature/answer-rowan-0123456789ab-20260914T010203Z-a1b2c3.json"
}
`
	if item.Path != wantPath {
		t.Fatalf("path = %q, want %q", item.Path, wantPath)
	}
	if got := string(item.Body); got != wantBody {
		t.Fatalf("body = %q, want %q", got, wantBody)
	}
	got, err := DecodeAnswer(item.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := Answer{Version: Version, Entry: "951/feature", Who: "rowan", Finding: "20260913T140102Z-a1b2c3.1", As: "dup", Of: "20260913T140102Z-a1b2c3.0", Head: testHead, At: testSubmission.At, Note: "same root cause", File: wantPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeAnswer = %#v, want %#v", got, want)
	}
}

func TestPolicyItemGoldenRoundTrip(t *testing.T) {
	item, err := PolicyItem("951", "glenn", []string{"emma", "stella"}, []string{"freddy"}, "2026-09-15T00:00:00Z", "freddy's harness is down", testHead, testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	const wantPath = "reviews/951/policy-glenn-0123456789ab-20260914T010203Z-a1b2c3.json"
	const wantBody = `{
  "version": 1,
  "entry": "951",
  "who": "glenn",
  "readers": [
    "emma",
    "stella"
  ],
  "reserved": [
    "freddy"
  ],
  "deadline": "2026-09-15T00:00:00Z",
  "reason": "freddy's harness is down",
  "head": "0123456789abcdef0123456789abcdef01234567",
  "at": "2026-09-14T01:02:03Z",
  "file": "reviews/951/policy-glenn-0123456789ab-20260914T010203Z-a1b2c3.json"
}
`
	if item.Path != wantPath {
		t.Fatalf("path = %q, want %q", item.Path, wantPath)
	}
	if got := string(item.Body); got != wantBody {
		t.Fatalf("body = %q, want %q", got, wantBody)
	}
	got, err := DecodePolicy(item.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := Policy{Version: Version, Entry: "951", Who: "glenn", Readers: []string{"emma", "stella"}, Reserved: []string{"freddy"}, Deadline: "2026-09-15T00:00:00Z", Reason: "freddy's harness is down", Head: testHead, At: testSubmission.At, File: wantPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodePolicy = %#v, want %#v", got, want)
	}
}

func TestDecodeV1RefusesHostileRecordShapes(t *testing.T) {
	answer := mustAnswerItem(t)
	policy := mustPolicyItem(t)
	cases := []struct {
		name   string
		decode func([]byte) error
		body   []byte
		want   string
	}{
		{"answer duplicate", decodeAnswer, duplicateField(answer.Body, "who", `"other"`), "duplicate"},
		{"policy duplicate", decodePolicy, duplicateField(policy.Body, "reason", `"again"`), "duplicate"},
		{"answer unknown", decodeAnswer, insertAfterVersion(answer.Body, `  "future": true,`), "unknown"},
		{"policy missing", decodePolicy, removeLine(policy.Body, `  "reason": "freddy's harness is down",`), "no reason"},
		{"answer trailing", decodeAnswer, append(append([]byte(nil), answer.Body...), []byte("{}")...), "trailing"},
		{"policy null array", decodePolicy, replaceOnce(policy.Body, `[
    "emma",
    "stella"
  ]`, `null`), "readers is null"},
		{"policy scalar array", decodePolicy, replaceOnce(policy.Body, `[
    "freddy"
  ]`, `"freddy"`), "reserved is not an array"},
		{"policy null member", decodePolicy, replaceOnce(policy.Body, `[
    "freddy"
  ]`, `[
    null
  ]`), "reserved[0] is null"},
		{"answer string type", decodeAnswer, replaceOnce(answer.Body, `"same root cause"`, `false`), "note is not a string"},
		{"answer path mismatch", decodeAnswer, replaceOnce(answer.Body, `answer-rowan-0123456789ab-`, `answer-rowan-fedcba987654-`), "does not match"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.decode(tt.body)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("decode error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeVersionPrecedesOtherValidation(t *testing.T) {
	answer := mustAnswerItem(t)
	base := replaceOnce(answer.Body, `"version": 1`, `"version": 2`)
	for _, body := range [][]byte{
		insertAfter(base, []byte("  \"version\": 2,\n"), `  "future": true,`),
		duplicateField(base, "who", `"other"`),
	} {
		_, err := DecodeAnswer(body)
		if err == nil || !strings.Contains(err.Error(), "version 2 is not supported") {
			t.Fatalf("DecodeAnswer error = %v, want unsupported version", err)
		}
	}
}

func TestAnswerDispositionIsClosed(t *testing.T) {
	for _, tt := range []struct {
		as, of string
		want   bool
	}{
		{"fixed", "", true},
		{"declined", "", true},
		{"dup", "other.1", true},
		{"fixed", "other.1", false},
		{"declined", "other.1", false},
		{"dup", "", false},
		{"close", "", false},
	} {
		t.Run(tt.as+"/"+tt.of, func(t *testing.T) {
			_, err := AnswerItem("951", "rowan", "finding.1", tt.as, tt.of, testHead, "", testSubmission)
			if (err == nil) != tt.want {
				t.Fatalf("AnswerItem(%q, %q) error = %v, want success=%t", tt.as, tt.of, err, tt.want)
			}
		})
	}
}

func TestPolicyCodecDoesNotDecideListMembership(t *testing.T) {
	item, err := PolicyItem("951", "glenn", []string{"emma", "emma"}, []string{"emma"}, "2026-09-15T00:00:00Z", "verbatim", testHead, testSubmission)
	if err != nil {
		t.Fatalf("PolicyItem rejected list membership reserved for policy/fold layers: %v", err)
	}
	got, err := DecodePolicy(item.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Readers, []string{"emma", "emma"}) || !reflect.DeepEqual(got.Reserved, []string{"emma"}) || got.Deadline != "2026-09-15T00:00:00Z" || got.Reason != "verbatim" {
		t.Fatalf("DecodePolicy changed policy data: %#v", got)
	}
}

func TestPolicyItemWritesNonNullEmptyArrays(t *testing.T) {
	item, err := PolicyItem("951", "glenn", nil, nil, "2026-09-15T00:00:00Z", "", testHead, testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(item.Body, []byte(`"readers": []`)) || !bytes.Contains(item.Body, []byte(`"reserved": []`)) {
		t.Fatalf("PolicyItem encoded null list: %s", item.Body)
	}
	if _, err := DecodePolicy(item.Body); err != nil {
		t.Fatalf("DecodePolicy rejected its own empty lists: %v", err)
	}
}

func TestPolicyDeadlineIsExactUTCStamp(t *testing.T) {
	for _, deadline := range []string{"", "tomorrow", "2026-09-15T00:00:00+00:00", "2026-09-15T00:00:00.000Z"} {
		t.Run(deadline, func(t *testing.T) {
			_, err := PolicyItem("951", "glenn", nil, nil, deadline, "", testHead, testSubmission)
			if err == nil || !strings.Contains(err.Error(), "deadline") || !strings.Contains(err.Error(), "UTC stamp") {
				t.Fatalf("PolicyItem deadline %q error = %v", deadline, err)
			}
		})
	}
	item, err := PolicyItem("951", "glenn", nil, nil, "2026-09-15T00:00:00Z", "", testHead, testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePolicy(item.Body)
	if err != nil || got.Deadline != "2026-09-15T00:00:00Z" {
		t.Fatalf("DecodePolicy valid deadline = %#v, %v", got, err)
	}
	invalid := replaceOnce(item.Body, `"deadline": "2026-09-15T00:00:00Z"`, `"deadline": "tomorrow"`)
	if _, err := DecodePolicy(invalid); err == nil || !strings.Contains(err.Error(), "deadline") || !strings.Contains(err.Error(), "UTC stamp") {
		t.Fatalf("DecodePolicy malformed deadline error = %v", err)
	}
}

func TestDecodeIdentityMustMatchPathAndStamp(t *testing.T) {
	answer := mustAnswerItem(t)
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"entry path", replaceOnce(answer.Body, `"entry": "951/feature"`, `"entry": "952/feature"`), "not under"},
		{"head path", replaceOnce(answer.Body, testHead, "abcdefabcdefabcdefabcdefabcdefabcdefabcd"), "does not match"},
		{"invalid stamp", replaceOnce(answer.Body, testSubmission.At, "2026-09-14T01:02:03+00:00"), "not a UTC stamp"},
		{"upper random", replaceOnce(answer.Body, "a1b2c3.json", "A1B2C3.json"), "random suffix"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeAnswer(tt.body)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodeAnswer error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCodecConfinesEntryDirectoryAndUTF8(t *testing.T) {
	for _, entry := range []string{".", ".."} {
		t.Run("entry="+entry, func(t *testing.T) {
			_, err := AnswerItem(entry, "rowan", "finding.1", "fixed", "", testHead, "", testSubmission)
			if err == nil || !strings.Contains(err.Error(), "one reviews directory") {
				t.Fatalf("AnswerItem(%q) error = %v", entry, err)
			}
		})
	}
	invalid := string([]byte{'o', 'k', 0xff})
	if _, err := AnswerItem("951", "rowan", "finding.1", "fixed", "", testHead, invalid, testSubmission); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("AnswerItem invalid UTF-8 error = %v", err)
	}

	answer := mustAnswerItem(t)
	invalidBody := replaceOnce(answer.Body, "same root cause", string([]byte{'o', 'k', 0xff}))
	if _, err := DecodeAnswer(invalidBody); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("DecodeAnswer invalid UTF-8 error = %v", err)
	}
	surrogate := replaceOnce(answer.Body, `"same root cause"`, `"\ud800"`)
	if _, err := DecodeAnswer(surrogate); err == nil || !strings.Contains(err.Error(), "unpaired high surrogate") {
		t.Fatalf("DecodeAnswer unpaired surrogate error = %v", err)
	}
	pair := replaceOnce(answer.Body, `"same root cause"`, `"\ud83d\udc31"`)
	decoded, err := DecodeAnswer(pair)
	if err != nil || decoded.Note != "🐱" {
		t.Fatalf("DecodeAnswer surrogate pair = %#v, %v", decoded, err)
	}
	literal, err := AnswerItem("951", "rowan", "finding.1", "fixed", "", testHead, "café 🐱", testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeAnswer(literal.Body)
	if err != nil || decoded.Note != "café 🐱" {
		t.Fatalf("DecodeAnswer literal Unicode = %#v, %v", decoded, err)
	}
}

func mustAnswerItem(t *testing.T) merge.Item {
	t.Helper()
	item, err := AnswerItem("951/feature", "rowan", "20260913T140102Z-a1b2c3.1", "dup", "20260913T140102Z-a1b2c3.0", testHead, "same root cause", testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func mustPolicyItem(t *testing.T) merge.Item {
	t.Helper()
	item, err := PolicyItem("951", "glenn", []string{"emma", "stella"}, []string{"freddy"}, "2026-09-15T00:00:00Z", "freddy's harness is down", testHead, testSubmission)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func decodeAnswer(body []byte) error {
	_, err := DecodeAnswer(body)
	return err
}

func decodePolicy(body []byte) error {
	_, err := DecodePolicy(body)
	return err
}

func duplicateField(body []byte, field, value string) []byte {
	needle := []byte(`  "` + field + `": `)
	at := bytes.Index(body, needle)
	if at < 0 {
		panic("field not found")
	}
	out := append([]byte(nil), body[:at]...)
	out = append(out, []byte(`  "`+field+`": `+value+",\n")...)
	return append(out, body[at:]...)
}

func insertAfterVersion(body []byte, line string) []byte {
	return insertAfter(body, []byte("  \"version\": 1,\n"), line)
}

func insertAfter(body, needle []byte, line string) []byte {
	at := bytes.Index(body, needle)
	if at < 0 {
		panic("text not found")
	}
	at += len(needle)
	out := append([]byte(nil), body[:at]...)
	out = append(out, []byte(line+"\n")...)
	return append(out, body[at:]...)
}

func removeLine(body []byte, line string) []byte {
	return bytes.Replace(body, []byte(line+"\n"), nil, 1)
}

func replaceOnce(body []byte, old, new string) []byte {
	out := bytes.Replace(body, []byte(old), []byte(new), 1)
	if bytes.Equal(out, body) {
		panic("text not found")
	}
	return out
}
