// Package review owns nova-review's immutable answer and policy record codecs.
package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

const Version = 1

const reviewsDir = "reviews"

// Answer is the author's disposition beside one finding. It is not the finding's closure.
type Answer struct {
	Version int    `json:"version"`
	Entry   string `json:"entry"`
	Who     string `json:"who"`
	Finding string `json:"finding"`
	As      string `json:"as"`
	Of      string `json:"of"`
	Head    string `json:"head"`
	At      string `json:"at"`
	Note    string `json:"note"`
	File    string `json:"file"`
}

// Policy is the actor's immutable reader/waiver record. List membership, timing, and
// authority are fold and verb decisions, not codec decisions.
type Policy struct {
	Version  int      `json:"version"`
	Entry    string   `json:"entry"`
	Who      string   `json:"who"`
	Readers  []string `json:"readers"`
	Reserved []string `json:"reserved"`
	Deadline string   `json:"deadline"`
	Reason   string   `json:"reason"`
	Head     string   `json:"head"`
	At       string   `json:"at"`
	File     string   `json:"file"`
}

// AnswerFile is the one path an answer record may claim. entry is the raw entry id; its
// one-level directory spelling belongs to internal/merge.
func AnswerFile(entry, who, head string, sub merge.Submission) string {
	return recordFile("answer", entry, who, head, sub)
}

// PolicyFile is the one path a policy record may claim.
func PolicyFile(entry, who, head string, sub merge.Submission) string {
	return recordFile("policy", entry, who, head, sub)
}

func recordFile(kind, entry, who, head string, sub merge.Submission) string {
	return reviewsDir + "/" + merge.EntryDirName(entry) + "/" +
		kind + "-" + merge.RecordName(who) + "-" + merge.Short(head) + "-" + sub.ID() + ".json"
}

// AnswerItem encodes one complete immutable answer record. Publication remains the
// outbox layer's responsibility.
func AnswerItem(entry, who, finding, as, of, head, note string, sub merge.Submission) (merge.Item, error) {
	rec := Answer{Version: Version, Entry: entry, Who: who, Finding: finding, As: as, Of: of,
		Head: head, At: sub.At, Note: note, File: AnswerFile(entry, who, head, sub)}
	if err := validAnswer(rec); err != nil {
		return merge.Item{}, err
	}
	return encode(rec.File, rec)
}

// PolicyItem encodes one complete immutable policy record. It deliberately does not decide
// who may be named, whether lists overlap, or whether the deadline has passed.
func PolicyItem(entry, who string, readers, reserved []string, deadline, reason, head string, sub merge.Submission) (merge.Item, error) {
	rec := Policy{Version: Version, Entry: entry, Who: who, Readers: copyStrings(readers),
		Reserved: copyStrings(reserved), Deadline: deadline, Reason: reason, Head: head,
		At: sub.At, File: PolicyFile(entry, who, head, sub)}
	if err := validPolicy(rec); err != nil {
		return merge.Item{}, err
	}
	return encode(rec.File, rec)
}

// copyStrings preserves the policy's caller-supplied list exactly, while encoding a nil
// Go slice as the required non-null JSON empty array.
func copyStrings(values []string) []string {
	return append([]string{}, values...)
}

// DecodeAnswer strictly decodes exactly one v1 answer record.
func DecodeAnswer(body []byte) (Answer, error) {
	fields, err := decodeV1(body, answerFields)
	if err != nil {
		return Answer{}, err
	}
	rec := Answer{Version: Version}
	if rec.Entry, err = stringField(fields, "entry"); err != nil {
		return Answer{}, err
	}
	if rec.Who, err = stringField(fields, "who"); err != nil {
		return Answer{}, err
	}
	if rec.Finding, err = stringField(fields, "finding"); err != nil {
		return Answer{}, err
	}
	if rec.As, err = stringField(fields, "as"); err != nil {
		return Answer{}, err
	}
	if rec.Of, err = stringField(fields, "of"); err != nil {
		return Answer{}, err
	}
	if rec.Head, err = stringField(fields, "head"); err != nil {
		return Answer{}, err
	}
	if rec.At, err = stringField(fields, "at"); err != nil {
		return Answer{}, err
	}
	if rec.Note, err = stringField(fields, "note"); err != nil {
		return Answer{}, err
	}
	if rec.File, err = stringField(fields, "file"); err != nil {
		return Answer{}, err
	}
	if err := validAnswer(rec); err != nil {
		return Answer{}, err
	}
	return rec, nil
}

// DecodePolicy strictly decodes exactly one v1 policy record.
func DecodePolicy(body []byte) (Policy, error) {
	fields, err := decodeV1(body, policyFields)
	if err != nil {
		return Policy{}, err
	}
	rec := Policy{Version: Version}
	if rec.Entry, err = stringField(fields, "entry"); err != nil {
		return Policy{}, err
	}
	if rec.Who, err = stringField(fields, "who"); err != nil {
		return Policy{}, err
	}
	if rec.Readers, err = stringsField(fields, "readers"); err != nil {
		return Policy{}, err
	}
	if rec.Reserved, err = stringsField(fields, "reserved"); err != nil {
		return Policy{}, err
	}
	if rec.Deadline, err = stringField(fields, "deadline"); err != nil {
		return Policy{}, err
	}
	if rec.Reason, err = stringField(fields, "reason"); err != nil {
		return Policy{}, err
	}
	if rec.Head, err = stringField(fields, "head"); err != nil {
		return Policy{}, err
	}
	if rec.At, err = stringField(fields, "at"); err != nil {
		return Policy{}, err
	}
	if rec.File, err = stringField(fields, "file"); err != nil {
		return Policy{}, err
	}
	if err := validPolicy(rec); err != nil {
		return Policy{}, err
	}
	return rec, nil
}

var answerFields = fieldSet("version", "entry", "who", "finding", "as", "of", "head", "at", "note", "file")
var policyFields = fieldSet("version", "entry", "who", "readers", "reserved", "deadline", "reason", "head", "at", "file")

func fieldSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func decodeV1(body []byte, allowed map[string]bool) (map[string]json.RawMessage, error) {
	fields, duplicate, err := objectFields(body)
	if err != nil {
		return nil, err
	}
	version, ok := fields["version"]
	if !ok {
		return nil, fmt.Errorf("record has no version")
	}
	if isNull(version) {
		return nil, fmt.Errorf("record version is null")
	}
	var n int
	if err := json.Unmarshal(version, &n); err != nil || n != Version {
		return nil, fmt.Errorf("record version %s is not supported", string(version))
	}
	if duplicate != "" {
		return nil, fmt.Errorf("record has duplicate field %q", duplicate)
	}
	for name := range fields {
		if !allowed[name] {
			return nil, fmt.Errorf("record has unknown field %q", name)
		}
	}
	for name := range allowed {
		if _, ok := fields[name]; !ok {
			return nil, fmt.Errorf("record has no %s", name)
		}
	}
	return fields, nil
}

func objectFields(body []byte) (map[string]json.RawMessage, string, error) {
	if !utf8.Valid(body) {
		return nil, "", fmt.Errorf("record is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	token, err := dec.Token()
	if err != nil {
		return nil, "", err
	}
	if token != json.Delim('{') {
		return nil, "", fmt.Errorf("record is not an object")
	}
	fields := map[string]json.RawMessage{}
	var duplicate string
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return nil, "", err
		}
		name, ok := token.(string)
		if !ok {
			return nil, "", fmt.Errorf("record field name is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, "", err
		}
		if _, exists := fields[name]; exists {
			if duplicate == "" {
				duplicate = name
			}
			continue
		}
		fields[name] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, "", err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, "", fmt.Errorf("record has trailing JSON")
		}
		return nil, "", err
	}
	return fields, duplicate, nil
}

func stringField(fields map[string]json.RawMessage, name string) (string, error) {
	raw := bytes.TrimSpace(fields[name])
	if isNull(raw) {
		return "", fmt.Errorf("record field %s is null", name)
	}
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("record field %s is not a string", name)
	}
	if err := strictJSONString(raw); err != nil {
		return "", fmt.Errorf("record field %s is not a lossless string: %w", name, err)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("record field %s is not a string", name)
	}
	return value, nil
}

func stringsField(fields map[string]json.RawMessage, name string) ([]string, error) {
	raw := bytes.TrimSpace(fields[name])
	if isNull(raw) {
		return nil, fmt.Errorf("record field %s is null", name)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("record field %s is not an array", name)
	}
	out := make([]string, len(values))
	for i, value := range values {
		value = bytes.TrimSpace(value)
		if isNull(value) {
			return nil, fmt.Errorf("record field %s[%d] is null", name, i)
		}
		if err := strictJSONString(value); err != nil {
			return nil, fmt.Errorf("record field %s[%d] is not a lossless string: %w", name, i, err)
		}
		if err := json.Unmarshal(value, &out[i]); err != nil {
			return nil, fmt.Errorf("record field %s[%d] is not a string", name, i)
		}
	}
	return out, nil
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func validAnswer(rec Answer) error {
	if err := validText("entry", rec.Entry); err != nil {
		return err
	}
	if err := validText("who", rec.Who); err != nil {
		return err
	}
	if err := validText("finding", rec.Finding); err != nil {
		return err
	}
	if err := validText("as", rec.As); err != nil {
		return err
	}
	if err := validText("of", rec.Of); err != nil {
		return err
	}
	if err := validText("head", rec.Head); err != nil {
		return err
	}
	if err := validText("at", rec.At); err != nil {
		return err
	}
	if err := validText("note", rec.Note); err != nil {
		return err
	}
	if err := validText("file", rec.File); err != nil {
		return err
	}
	if err := validIdentity(rec.Entry, rec.Who, rec.Head, rec.At, rec.File, "answer"); err != nil {
		return err
	}
	if strings.TrimSpace(rec.Finding) == "" {
		return fmt.Errorf("answer has no finding")
	}
	switch rec.As {
	case "fixed", "declined":
		if rec.Of != "" {
			return fmt.Errorf("answer as=%s must have empty of", rec.As)
		}
	case "dup":
		if strings.TrimSpace(rec.Of) == "" {
			return fmt.Errorf("answer as=dup needs of")
		}
	default:
		return fmt.Errorf("answer as %q is not fixed, declined or dup", rec.As)
	}
	return nil
}

func validPolicy(rec Policy) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"entry", rec.Entry}, {"who", rec.Who}, {"deadline", rec.Deadline},
		{"reason", rec.Reason}, {"head", rec.Head}, {"at", rec.At}, {"file", rec.File},
	} {
		if err := validText(field.name, field.value); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name   string
		values []string
	}{
		{"readers", rec.Readers}, {"reserved", rec.Reserved},
	} {
		for i, value := range field.values {
			if err := validText(fmt.Sprintf("%s[%d]", field.name, i), value); err != nil {
				return err
			}
		}
	}
	return validIdentity(rec.Entry, rec.Who, rec.Head, rec.At, rec.File, "policy")
}

func validText(name, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("record field %s is not valid UTF-8", name)
	}
	return nil
}

func validIdentity(entry, who, head, at, file, kind string) error {
	if strings.TrimSpace(entry) == "" {
		return fmt.Errorf("%s record has no entry", kind)
	}
	if strings.TrimSpace(who) == "" {
		return fmt.Errorf("%s record has no who", kind)
	}
	entryDir := merge.EntryDirName(entry)
	if entryDir == "." || entryDir == ".." || strings.Contains(entryDir, "/") {
		return fmt.Errorf("%s record entry %q does not name one reviews directory", kind, entry)
	}
	if !merge.IsSHA(head) {
		return fmt.Errorf("%s record head %q is not a full lower-case sha", kind, head)
	}
	parsed, err := time.Parse(merge.Stamp, at)
	if err != nil || parsed.Format(merge.Stamp) != at {
		return fmt.Errorf("%s record at %q is not a UTC stamp", kind, at)
	}
	parts := strings.Split(file, "/")
	if len(parts) != 3 || parts[0] != reviewsDir || parts[1] != entryDir {
		return fmt.Errorf("%s record file %q is not under reviews/%s", kind, file, entryDir)
	}
	template := recordFile(kind, entry, who, head, merge.Submission{At: at, Rand: "000000"})
	prefix := strings.TrimSuffix(template, "000000.json")
	if !strings.HasPrefix(file, prefix) || !strings.HasSuffix(file, ".json") {
		return fmt.Errorf("%s record file %q does not match its fields", kind, file)
	}
	rand := strings.TrimSuffix(strings.TrimPrefix(file, prefix), ".json")
	if !lowerHex(rand, 6) {
		return fmt.Errorf("%s record file %q has no six-character lower-case random suffix", kind, file)
	}
	if want := recordFile(kind, entry, who, head, merge.Submission{At: at, Rand: rand}); file != want {
		return fmt.Errorf("%s record file %q does not match its fields", kind, file)
	}
	return nil
}

func lowerHex(value string, n int) bool {
	if len(value) != n {
		return false
	}
	for i := range value {
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return false
		}
	}
	return true
}

// strictJSONString accepts the JSON string spelling only when decoding cannot replace an
// invalid UTF-8 byte sequence or unpaired UTF-16 surrogate with U+FFFD.
func strictJSONString(raw []byte) error {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return fmt.Errorf("not a string")
	}
	for i := 1; i < len(raw)-1; {
		if raw[i] < 0x20 {
			return fmt.Errorf("control byte")
		}
		if raw[i] == '\\' {
			i++
			if i >= len(raw)-1 {
				return fmt.Errorf("incomplete escape")
			}
			switch raw[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i++
			case 'u':
				u, ok := hexRune(raw[i+1 : min(i+5, len(raw)-1)])
				if !ok {
					return fmt.Errorf("bad unicode escape")
				}
				i += 5
				switch {
				case u >= 0xd800 && u <= 0xdbff:
					if i+6 > len(raw)-1 || raw[i] != '\\' || raw[i+1] != 'u' {
						return fmt.Errorf("unpaired high surrogate")
					}
					low, ok := hexRune(raw[i+2 : i+6])
					if !ok || low < 0xdc00 || low > 0xdfff {
						return fmt.Errorf("unpaired high surrogate")
					}
					i += 6
				case u >= 0xdc00 && u <= 0xdfff:
					return fmt.Errorf("unpaired low surrogate")
				}
			default:
				return fmt.Errorf("bad escape")
			}
			continue
		}
		rune, width := utf8.DecodeRune(raw[i : len(raw)-1])
		if rune == utf8.RuneError && width == 1 {
			return fmt.Errorf("invalid UTF-8")
		}
		i += width
	}
	return nil
}

func hexRune(raw []byte) (rune, bool) {
	if len(raw) != 4 {
		return 0, false
	}
	var value rune
	for _, b := range raw {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value += rune(b - '0')
		case b >= 'a' && b <= 'f':
			value += rune(b-'a') + 10
		case b >= 'A' && b <= 'F':
			value += rune(b-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func encode(file string, rec any) (merge.Item, error) {
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return merge.Item{}, err
	}
	body = append(body, '\n')
	return merge.Item{Path: file, Body: body}, nil
}
