// A question and the criteria it is answered against are ONE versioned pair,
// and the pair is carried through the call boundary rather than asserted on
// disk (Stella, 2026-09-19, repair 1 of the #1925 hold).
//
// The first shape of this shipped a question file naming a `criteria_file`, a
// `criteria_version` and its `state_fields`, and a parser that admitted those
// three keys and then DISCARDED them. Nothing loaded the criteria, nothing
// embedded it, nothing validated the state against the declared fields, and
// the only gate was a documentation test comparing two files on disk. Text
// parity is not ingestion: the bytes that went to the provider were the
// caller's state and the compact questions, exactly as before.
//
// LoadQuestionFile loads the pair, refusing a version that disagrees and a
// criteria path that leaves the question file's own directory. Payload is the
// gate in front of the provider: it validates the state against the declared
// typed fields and returns the bytes that go out -- the criteria first, the
// state second -- so a missing or mistyped field is a refusal BEFORE any call
// is made and before anything is spent.
package decide

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxCriteriaBytes caps the criteria a question may carry into a request. A
// criteria file is a page of prose; anything at this size is a mistake or a
// file the question did not mean to name.
const maxCriteriaBytes = 64 << 10

// StateField is one typed fact the asker must compute BEFORE the question is
// asked. Type is one of "string", "int" or "bool"; a field is required unless
// it says otherwise, because a criterion that turns on a fact the state may or
// may not carry is a criterion the provider must infer from prose.
type StateField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"`
}

// QuestionFile is a loaded question file: its questions, the criteria they are
// answered against, and the typed state fields the asker owes.
type QuestionFile struct {
	Path            string
	CriteriaVersion string
	CriteriaPath    string
	Criteria        string
	StateFields     []StateField
	Questions       map[string]Question
	// Machinery is the rules this question is answered UNDER, declared in the
	// versioned pair rather than matched on a question's name inside a verb.
	// Empty is the ordinary question: the provider's answer stands as it is.
	// MachineryWhoReads binds the file to readers.go's rules, which the call
	// boundary applies -- a settled designation before any client is built, and
	// a constraint over any advisory answer before it is printed or recorded.
	Machinery string
}

// questionFileWire is the envelope's metadata. The questions themselves go
// through ParseQuestions, which is the one parser for them.
type questionFileWire struct {
	CriteriaVersion string       `json:"criteria_version"`
	CriteriaFile    string       `json:"criteria_file"`
	StateFields     []StateField `json:"state_fields"`
	Machinery       string       `json:"machinery"`
}

// LoadQuestionFile reads a question file and the criteria file it names. A
// bare question file -- no criteria, no declared fields -- loads as it always
// did, so every caller that predates the pair keeps working.
func LoadQuestionFile(path string) (QuestionFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return QuestionFile{}, fmt.Errorf("decide: cannot read questions: %w", err)
	}
	qs, err := ParseQuestions(raw)
	if err != nil {
		return QuestionFile{}, err
	}
	var meta questionFileWire
	if err := json.Unmarshal(raw, &meta); err != nil {
		return QuestionFile{}, fmt.Errorf("decide: bad questions: %w", err)
	}
	f := QuestionFile{
		Path:            path,
		CriteriaVersion: strings.TrimSpace(meta.CriteriaVersion),
		CriteriaPath:    strings.TrimSpace(meta.CriteriaFile),
		StateFields:     meta.StateFields,
		Questions:       qs,
		Machinery:       strings.TrimSpace(meta.Machinery),
	}
	switch f.Machinery {
	case "":
	case MachineryWhoReads:
		if _, ok := f.Questions[ReadQuestion]; !ok {
			return QuestionFile{}, fmt.Errorf("decide: bad questions: %s declares machinery %q and asks no %q question; the machinery constrains that answer and there is none",
				path, f.Machinery, ReadQuestion)
		}
	default:
		return QuestionFile{}, fmt.Errorf("decide: bad questions: %s declares machinery %q, want %q or none", path, f.Machinery, MachineryWhoReads)
	}
	for _, sf := range f.StateFields {
		if strings.TrimSpace(sf.Name) == "" {
			return QuestionFile{}, fmt.Errorf("decide: bad questions: a state field has no name")
		}
		switch sf.Type {
		case "string", "int", "bool":
		default:
			return QuestionFile{}, fmt.Errorf("decide: bad questions: state field %q has type %q, want string, int or bool", sf.Name, sf.Type)
		}
	}
	if f.CriteriaPath == "" {
		if f.CriteriaVersion != "" {
			return QuestionFile{}, fmt.Errorf("decide: bad questions: %s names criteria_version %q and no criteria_file", path, f.CriteriaVersion)
		}
		return f, nil
	}
	body, err := readContained(filepath.Dir(path), f.CriteriaPath)
	if err != nil {
		return QuestionFile{}, err
	}
	f.Criteria = body
	if f.CriteriaVersion != "" {
		want := "version: " + f.CriteriaVersion
		if !strings.Contains(body, want) {
			return QuestionFile{}, fmt.Errorf("decide: bad questions: %s cites criteria_version %q and %s carries %q",
				filepath.Base(path), f.CriteriaVersion, f.CriteriaPath, criteriaVersionIn(body))
		}
	}
	return f, nil
}

// readContained reads a file the question NAMED, and only from inside the
// question file's own directory. An absolute path, a parent escape and a
// symlink pointing out are all the same refusal: a question file is not a way
// to read an unrelated local file and post it to a provider.
func readContained(dir, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("decide: bad questions: criteria_file %q is absolute; it must sit beside its question file", name)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("decide: bad questions: criteria_file %q leaves its question file's directory", name)
	}
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("decide: bad questions: cannot resolve %s: %w", dir, err)
	}
	full, err := filepath.EvalSymlinks(filepath.Join(base, clean))
	if err != nil {
		return "", fmt.Errorf("decide: bad questions: cannot read criteria_file %q: %w", name, err)
	}
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("decide: bad questions: criteria_file %q resolves outside its question file's directory", name)
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("decide: bad questions: cannot read criteria_file %q: %w", name, err)
	}
	if info.Size() > maxCriteriaBytes {
		return "", fmt.Errorf("decide: bad questions: criteria_file %q is %d bytes, over the %d-byte cap", name, info.Size(), maxCriteriaBytes)
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("decide: bad questions: cannot read criteria_file %q: %w", name, err)
	}
	return string(body), nil
}

// criteriaVersionIn reports the version line a criteria file actually carries,
// so a mismatch names both halves rather than only the one that was wanted.
func criteriaVersionIn(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "version:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return "no version line"
}

// Payload validates the state against the declared typed fields and returns
// the bytes that go to the provider: the criteria first, the state second. A
// required field the state does not carry, or carries with the wrong type, is
// a refusal HERE -- before the call, before the spend, before a confidence is
// attached to an answer given over evidence that was not there.
func (f QuestionFile) Payload(state string) (string, error) {
	fields := parseStateFields(state)
	for _, sf := range f.StateFields {
		raw, ok := fields[sf.Name]
		if !ok || strings.TrimSpace(raw) == "" {
			if sf.Optional {
				continue
			}
			return "", fmt.Errorf("decide: state does not carry required field %q (%s); refusing to ask over evidence that is not there", sf.Name, sf.Type)
		}
		if err := checkStateType(sf, raw); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(f.Criteria) == "" {
		return state, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# criteria (%s, version %s)\n", f.CriteriaPath, f.CriteriaVersion)
	b.WriteString(f.Criteria)
	if !strings.HasSuffix(f.Criteria, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# state\n")
	b.WriteString(state)
	return b.String(), nil
}

// parseStateFields reads the state's `key: value` lines. A state is a short
// typed record, not a document: a line with no colon is prose beside the
// record and is left alone.
func parseStateFields(state string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(state, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t#") {
			continue
		}
		if _, seen := out[key]; seen {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

// checkStateType refuses a value that is not the declared type, naming the
// type it wanted. "yes"/"no" are bools because that is how the askers write
// them; an int is an int.
func checkStateType(sf StateField, raw string) error {
	v := strings.TrimSpace(raw)
	switch sf.Type {
	case "bool":
		switch strings.ToLower(firstWord(v)) {
		case "yes", "no", "true", "false":
			return nil
		}
		return fmt.Errorf("decide: state field %q is %q, want a bool (yes or no)", sf.Name, v)
	case "int":
		if _, err := strconv.Atoi(firstWord(v)); err != nil {
			return fmt.Errorf("decide: state field %q is %q, want an int", sf.Name, v)
		}
		return nil
	default:
		return nil
	}
}

// firstWord is the value's own token: an asker writes `security_shaped_package:
// yes -- this is a guard on a destructive path`, and the fact is the first
// word with the reason beside it.
func firstWord(v string) string {
	for i, r := range v {
		if r == ' ' || r == '\t' || r == ',' {
			return v[:i]
		}
	}
	return v
}
