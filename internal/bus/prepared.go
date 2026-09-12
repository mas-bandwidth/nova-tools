package bus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PreparedSchema is the exact schema identifier for prepared bus delivery artifacts.
const PreparedSchema = "nova.bus.prepared/1"

// PreparedArtifact is the complete, self-contained JSON artifact representing a
// prepared note before delivery or mutation on the bus.
type PreparedArtifact struct {
	Schema string `json:"schema"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	Note   string `json:"note"`
	SHA256 string `json:"sha256"`
}

// MakePreparedArtifact constructs a valid PreparedArtifact from a Prepared note.
// It verifies that the rendered note ends with LF and computes the full sha256 digest.
func MakePreparedArtifact(p Prepared) (PreparedArtifact, error) {
	note := p.Note.Render()
	if !strings.HasSuffix(note, "\n") {
		return PreparedArtifact{}, errors.New("rendered note must end with LF")
	}
	h := sha256.Sum256([]byte(note))
	digest := hex.EncodeToString(h[:])
	return PreparedArtifact{
		Schema: PreparedSchema,
		ID:     p.Note.Header.ID,
		Path:   p.Path,
		Note:   note,
		SHA256: digest,
	}, nil
}

// RenderPreparedArtifact returns the JSON artifact string ending with LF.
func RenderPreparedArtifact(p Prepared) (string, error) {
	art, err := MakePreparedArtifact(p)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(art)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func checkJSONNoDuplicates(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return errors.New("expected string key in JSON object")
			}
			if seen[key] {
				return fmt.Errorf("duplicate key %q in JSON object", key)
			}
			seen[key] = true
			if err := checkJSONNoDuplicates(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
	case '[':
		for dec.More() {
			if err := checkJSONNoDuplicates(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
	}
	return nil
}

// ValidatePreparedArtifact parses and validates a raw JSON artifact against the current
// bus configuration, participant roster, and deterministic ID invariants.
func ValidatePreparedArtifact(raw []byte, busDir string, c *Config, as string) (PreparedArtifact, Prepared, error) {
	var art PreparedArtifact

	decCheck := json.NewDecoder(bytes.NewReader(raw))
	if err := checkJSONNoDuplicates(decCheck); err != nil {
		return art, Prepared{}, fmt.Errorf("malformed prepared artifact: %w", err)
	}
	var extra json.RawMessage
	if err := decCheck.Decode(&extra); !errors.Is(err, io.EOF) {
		return art, Prepared{}, errors.New("malformed prepared artifact: more than one JSON value")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&art); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return art, Prepared{}, errors.New("malformed prepared artifact: unknown field")
		}
		return art, Prepared{}, fmt.Errorf("malformed prepared artifact: %w", err)
	}
	if art.Schema != PreparedSchema {
		return art, Prepared{}, fmt.Errorf("unknown prepared artifact schema %q (want %q)", art.Schema, PreparedSchema)
	}
	if len(art.SHA256) != 64 {
		return art, Prepared{}, fmt.Errorf("invalid sha256 in prepared artifact: %q (want 64 lowercase hex digits)", art.SHA256)
	}
	for _, r := range art.SHA256 {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return art, Prepared{}, fmt.Errorf("invalid sha256 in prepared artifact: %q (must be lowercase hex digits)", art.SHA256)
		}
	}
	if !strings.HasSuffix(art.Note, "\n") {
		return art, Prepared{}, errors.New("prepared note must end with LF")
	}
	h := sha256.Sum256([]byte(art.Note))
	actualSHA := hex.EncodeToString(h[:])
	if actualSHA != art.SHA256 {
		return art, Prepared{}, fmt.Errorf("sha256 digest mismatch: artifact says %s, note hashes to %s", art.SHA256, actualSHA)
	}
	if as == "" {
		return art, Prepared{}, errors.New("--as is required with --prepared")
	}
	me, known := c.Lookup(as)
	if !known {
		return art, Prepared{}, fmt.Errorf("--as %q names no one on this bus (known: %s)", as, strings.Join(c.KnownNames(), "; "))
	}
	if me.Lane == "" {
		return art, Prepared{}, fmt.Errorf("%q has no lane on this bus, so has nowhere to send from", me.Name)
	}

	// The prepared path must be inside the sender's own lane
	if !strings.HasPrefix(art.Path, me.Lane+"/") {
		return art, Prepared{}, fmt.Errorf("prepared path %q is outside lane %q", art.Path, me.Lane)
	}
	fullPath := filepath.Join(busDir, filepath.FromSlash(art.Path))
	if err := insideRoot(busDir, fullPath); err != nil {
		return art, Prepared{}, err
	}

	// Parse the note content
	n, parseProblems := ParseNoteAll(art.Path, art.Note)
	if len(parseProblems) > 0 {
		return art, Prepared{}, problemsOf(parseProblems)
	}
	if n.Header.ID != art.ID {
		return art, Prepared{}, fmt.Errorf("note header id %q does not match artifact id %q", n.Header.ID, art.ID)
	}
	headerProblems := n.Header.Problems(c)
	if len(headerProblems) > 0 {
		return art, Prepared{}, problemsOf(headerProblems)
	}
	sender, senderKnown := c.ResolveOne(n.Header.From)
	if !senderKnown || sender.Name != me.Name {
		return art, Prepared{}, fmt.Errorf("--as %q, but note's From line says %q; send does not send one line's note as another", me.Name, n.Header.From)
	}
	if n.Lane != me.Lane {
		return art, Prepared{}, fmt.Errorf("note lane %q does not match sender lane %q", n.Lane, me.Lane)
	}

	// Check deterministic ID calculation
	expectedID, err := AssignID(c, me, n.Header, n.Body, n.Header.Date)
	if err != nil {
		return art, Prepared{}, err
	}
	if expectedID != art.ID {
		return art, Prepared{}, fmt.Errorf("note id %q does not match deterministic id %q", art.ID, expectedID)
	}

	// Check canonical rendering equality
	if n.Render() != art.Note {
		return art, Prepared{}, errors.New("note content does not match canonical rendering")
	}

	p := Prepared{
		Note:    n,
		Sender:  me,
		Path:    art.Path,
		Index:   IndexEntryFor(c, n),
		Message: me.Slug() + ": " + n.Header.Subject,
	}
	return art, p, nil
}

// queryRemote checks whether the prepared note and its INDEX entry already exist on the remote tracking ref.
func queryRemote(busDir, ref string, p Prepared, art PreparedArtifact) (bool, string, error) {
	remoteNote, errNote := git(busDir, "show", ref+":"+art.Path)
	remoteIndex, errIndex := git(busDir, "show", ref+":"+IndexPath(p.Sender.Lane))

	wantIndexLine := IndexLine(p.Index)
	indexCount := 0
	indexMatch := false

	if errIndex == nil {
		for _, line := range strings.Split(remoteIndex, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fields := strings.Split(line, "\t")
			if len(fields) > 0 && fields[0] == art.ID {
				indexCount++
				if len(fields) > 1 && fields[1] != art.Path {
					return false, "", fmt.Errorf("the id %q is already on remote on %s, not %s", art.ID, fields[1], art.Path)
				}
				if line != wantIndexLine {
					return false, "", fmt.Errorf("remote index in %s has conflicting record for %s (want %q, got %q)", IndexPath(p.Sender.Lane), art.ID, wantIndexLine, line)
				}
				indexMatch = true
			}
		}
	}

	if indexCount > 1 {
		return false, "", fmt.Errorf("remote index in %s has multiple records for %s", IndexPath(p.Sender.Lane), art.ID)
	}

	if indexMatch {
		if errNote != nil {
			return false, "", fmt.Errorf("remote index carries %q but note %s is absent remotely", art.ID, art.Path)
		}
		if remoteNote != art.Note {
			return false, "", fmt.Errorf("remote note at %s has different content than the prepared note", art.Path)
		}
		commitOut, _ := git(busDir, "log", "-1", "--format=%H", ref, "--", art.Path)
		commitSHA := strings.TrimSpace(commitOut)
		if commitSHA == "" {
			commitSHA, _ = ResolveCommit(busDir, ref)
		}
		return true, commitSHA, nil
	}

	if errNote == nil {
		return false, "", fmt.Errorf("remote note at %s exists but is not in the remote index", art.Path)
	}

	logOut, _ := git(busDir, "log", "-1", "--format=%H", ref, "--grep=Nova-Bus: send "+art.ID)
	if strings.TrimSpace(logOut) != "" {
		return false, "", fmt.Errorf("the id %q was already committed on remote with different path or index", art.ID)
	}

	return false, "", nil
}

// verifyPermittedDeltas verifies that any dirty changes in the working tree or staged index
// belong strictly to this prepared send, refusing any unrelated content or paths.
func verifyPermittedDeltas(busDir, ref string, p Prepared, art PreparedArtifact) error {
	out, err := git(busDir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}

	refIndexPath := IndexPath(p.Sender.Lane)
	refIndex, err := git(busDir, "show", ref+":"+refIndexPath)
	if err != nil {
		refIndex = ""
	}
	wantIndexLine := IndexLine(p.Index)
	expectedAppendedIndex := refIndex
	if expectedAppendedIndex != "" && !strings.HasSuffix(expectedAppendedIndex, "\n") {
		expectedAppendedIndex += "\n"
	}
	expectedAppendedIndex += wantIndexLine + "\n"

	refAttrs, err := git(busDir, "show", ref+":"+AttributesName)
	if err != nil {
		refAttrs = ""
	}
	expectedAttrs, _ := ExpectedMergeAttributes(refAttrs)

	permitted := map[string]bool{
		art.Path:       true,
		refIndexPath:   true,
		AttributesName: true,
	}

	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		status, path := rec[:2], rec[3:]
		if strings.ContainsAny(status, "RC") {
			if i+1 < len(recs) {
				i++
			}
			return fmt.Errorf("unrelated dirty rename or copy in working tree: %s", path)
		}
		if !permitted[path] {
			return fmt.Errorf("unrelated dirty file in working tree: %s", path)
		}

		switch path {
		case art.Path:
			full := filepath.Join(busDir, filepath.FromSlash(art.Path))
			if b, err := os.ReadFile(full); err == nil {
				if string(b) != art.Note {
					return fmt.Errorf("unrelated dirty changes in %s; refusing to publish", art.Path)
				}
			}
			if status[0] != ' ' && status[0] != '?' {
				staged, err := git(busDir, "show", ":"+art.Path)
				if err == nil && staged != art.Note {
					return fmt.Errorf("unrelated staged changes in %s; refusing to publish", art.Path)
				}
			}
		case refIndexPath:
			full := filepath.Join(busDir, filepath.FromSlash(refIndexPath))
			b, readErr := os.ReadFile(full)
			if refIndex != "" {
				if readErr != nil {
					return fmt.Errorf("unrelated deletion of %s; refusing to publish", refIndexPath)
				}
				s := string(b)
				if s != refIndex && s != expectedAppendedIndex {
					return fmt.Errorf("unrelated dirty changes in %s; refusing to publish", refIndexPath)
				}
			} else {
				if readErr == nil {
					s := string(b)
					if s != "" && s != wantIndexLine && s != wantIndexLine+"\n" {
						return fmt.Errorf("unrelated dirty changes in %s; refusing to publish", refIndexPath)
					}
				}
			}
			if status[0] != ' ' && status[0] != '?' {
				staged, err := git(busDir, "show", ":"+refIndexPath)
				if refIndex != "" {
					if err != nil || (staged != refIndex && staged != expectedAppendedIndex) {
						return fmt.Errorf("unrelated staged changes in %s; refusing to publish", refIndexPath)
					}
				} else {
					if err == nil && staged != "" && staged != wantIndexLine && staged != wantIndexLine+"\n" {
						return fmt.Errorf("unrelated staged changes in %s; refusing to publish", refIndexPath)
					}
				}
			}
		case AttributesName:
			full := filepath.Join(busDir, AttributesName)
			b, readErr := os.ReadFile(full)
			if refAttrs != "" {
				if readErr != nil || (string(b) != refAttrs && string(b) != expectedAttrs) {
					return fmt.Errorf("unrelated dirty changes in %s; refusing to publish", AttributesName)
				}
			} else {
				if readErr == nil && string(b) != expectedAttrs {
					return fmt.Errorf("unrelated dirty changes in %s; refusing to publish", AttributesName)
				}
			}
			if status[0] != ' ' && status[0] != '?' {
				staged, err := git(busDir, "show", ":"+AttributesName)
				if refAttrs != "" {
					if err != nil || (staged != refAttrs && staged != expectedAttrs) {
						return fmt.Errorf("unrelated staged changes in %s; refusing to publish", AttributesName)
					}
				} else {
					if err == nil && staged != expectedAttrs {
						return fmt.Errorf("unrelated staged changes in %s; refusing to publish", AttributesName)
					}
				}
			}
		}
	}
	return nil
}

// SendPreparedArtifact publishes or confirms delivery of a prepared note artifact.
func SendPreparedArtifact(busDir, remote, branch string, p Prepared, art PreparedArtifact, attempts int) (PushResult, error) {
	if attempts < 1 {
		return PushResult{}, fmt.Errorf("attempts must be at least 1, got %d", attempts)
	}
	if err := ValidGitArg("remote", remote); err != nil {
		return PushResult{}, err
	}
	if err := ValidGitArg("branch", branch); err != nil {
		return PushResult{}, err
	}

	on, err := CurrentBranch(busDir)
	if err != nil {
		return PushResult{}, err
	}
	if on != branch {
		return PushResult{}, fmt.Errorf("the bus's checkout is on branch %q, not %q", on, branch)
	}

	if _, err := git(busDir, "fetch", remote, branch); err != nil {
		return PushResult{}, fmt.Errorf("the fetch that would say whether %s/%s holds this note failed: %w", remote, branch, err)
	}
	ref := trackingRef(busDir, remote, branch)

	// Step 1 & 2: Remote verification under checkout lock
	published, commitSHA, err := queryRemote(busDir, ref, p, art)
	if err != nil {
		return PushResult{}, err
	}
	if published {
		return PushResult{
			Commit:   commitSHA,
			Attempts: 0,
			Pushed:   true,
			State:    "already-published",
		}, nil
	}

	// Step 3 & 4: If absent remotely, refuse unrelated work and reconcile local attempt
	if err := verifyPermittedDeltas(busDir, ref, p, art); err != nil {
		return PushResult{}, err
	}

	countOut, err := git(busDir, "rev-list", "--count", ref+"..HEAD")
	if err != nil {
		return PushResult{}, err
	}
	ahead, _ := strconv.Atoi(strings.TrimSpace(countOut))

	var reusedCommit string
	wantTrailer := TrailerSend + " " + art.ID

	if ahead > 0 {
		refIndexPath := IndexPath(p.Sender.Lane)
		refIndex, err := git(busDir, "show", ref+":"+refIndexPath)
		if err != nil {
			refIndex = ""
		}
		wantIndexLine := IndexLine(p.Index)

		refAttrs, err := git(busDir, "show", ref+":"+AttributesName)
		if err != nil {
			refAttrs = ""
		}
		expectedAttrs, _ := ExpectedMergeAttributes(refAttrs)

		var refNonArtLines []string
		for _, l := range strings.Split(refIndex, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			refNonArtLines = append(refNonArtLines, l)
		}

		logOut, err := git(busDir, "log", "-z", "--format=%H%n%B", ref+"..HEAD")
		if err != nil {
			return PushResult{}, err
		}
		for _, rec := range strings.Split(logOut, "\x00") {
			if strings.TrimSpace(rec) == "" {
				continue
			}
			sha, body, _ := strings.Cut(strings.TrimLeft(rec, "\n"), "\n")
			sha = strings.TrimSpace(sha)
			if !HasExactTrailer(body, wantTrailer) {
				return PushResult{}, fmt.Errorf("branch has ahead commits that are not this prepared send; %s", pullRebaseAdvice)
			}
			diffOut, _ := git(busDir, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
			for _, touched := range strings.Split(strings.TrimSpace(diffOut), "\n") {
				touched = strings.TrimSpace(touched)
				if touched == "" {
					continue
				}
				if touched != art.Path && touched != refIndexPath && touched != AttributesName {
					return PushResult{}, fmt.Errorf("ahead commit %s touches unrelated path %s; %s", sha, touched, pullRebaseAdvice)
				}
			}
			noteInCommit, err := git(busDir, "show", sha+":"+art.Path)
			if err == nil && noteInCommit != art.Note {
				return PushResult{}, fmt.Errorf("ahead commit %s has conflicting note content for %s; %s", sha, art.Path, pullRebaseAdvice)
			}

			// Validate attributes in each commit
			commitAttrs, errAttr := git(busDir, "show", sha+":"+AttributesName)
			if refAttrs != "" {
				if errAttr != nil || (commitAttrs != refAttrs && commitAttrs != expectedAttrs) {
					return PushResult{}, fmt.Errorf("ahead commit %s has unrelated changes in %s; %s", sha, AttributesName, pullRebaseAdvice)
				}
			} else {
				if errAttr == nil && commitAttrs != expectedAttrs {
					return PushResult{}, fmt.Errorf("ahead commit %s has unrelated changes in %s; %s", sha, AttributesName, pullRebaseAdvice)
				}
			}

			// Validate index in each commit
			commitIndex, errIndex := git(busDir, "show", sha+":"+refIndexPath)
			if refIndex != "" && errIndex != nil {
				return PushResult{}, fmt.Errorf("ahead commit %s deleted index in %s; %s", sha, refIndexPath, pullRebaseAdvice)
			}
			if errIndex == nil {
				var commitNonArtLines []string
				artCount := 0
				for _, l := range strings.Split(commitIndex, "\n") {
					l = strings.TrimSpace(l)
					if l == "" {
						continue
					}
					f := strings.Split(l, "\t")
					if len(f) > 0 && f[0] == art.ID {
						artCount++
						if l != wantIndexLine {
							return PushResult{}, fmt.Errorf("ahead commit %s has conflicting index record for %s; %s", sha, art.ID, pullRebaseAdvice)
						}
					} else {
						commitNonArtLines = append(commitNonArtLines, l)
					}
				}
				if artCount > 1 {
					return PushResult{}, fmt.Errorf("ahead commit %s has multiple index records for %s; %s", sha, art.ID, pullRebaseAdvice)
				}
				if strings.Join(commitNonArtLines, "\n") != strings.Join(refNonArtLines, "\n") {
					return PushResult{}, fmt.Errorf("ahead commit %s has unrelated changes in %s; %s", sha, refIndexPath, pullRebaseAdvice)
				}
			}
		}

		headNote, errNote := git(busDir, "show", "HEAD:"+art.Path)
		if errNote != nil || headNote != art.Note {
			return PushResult{}, fmt.Errorf("ahead commits do not contain valid note %s; %s", art.Path, pullRebaseAdvice)
		}

		// Validate attributes at HEAD
		headAttrs, errAttr := git(busDir, "show", "HEAD:"+AttributesName)
		if refAttrs != "" {
			if errAttr != nil || (headAttrs != refAttrs && headAttrs != expectedAttrs) {
				return PushResult{}, fmt.Errorf("ahead commits have unrelated changes in %s; %s", AttributesName, pullRebaseAdvice)
			}
		} else {
			if errAttr == nil && headAttrs != expectedAttrs {
				return PushResult{}, fmt.Errorf("ahead commits have unrelated changes in %s; %s", AttributesName, pullRebaseAdvice)
			}
		}

		headIndex, errIndex := git(busDir, "show", "HEAD:"+refIndexPath)
		if refIndex != "" && errIndex != nil {
			return PushResult{}, fmt.Errorf("ahead commits deleted index in %s; %s", refIndexPath, pullRebaseAdvice)
		}
		if errIndex != nil {
			headIndex = ""
		}

		headHasIndex := false
		artIndexCount := 0
		var headNonArtLines []string
		for _, l := range strings.Split(headIndex, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			f := strings.Split(l, "\t")
			if len(f) > 0 && f[0] == art.ID {
				artIndexCount++
				if l == wantIndexLine {
					headHasIndex = true
				} else {
					return PushResult{}, fmt.Errorf("ahead commits have conflicting index record for %s; %s", art.ID, pullRebaseAdvice)
				}
			} else {
				headNonArtLines = append(headNonArtLines, l)
			}
		}

		if artIndexCount > 1 {
			return PushResult{}, fmt.Errorf("ahead commits have multiple index records for %s; %s", art.ID, pullRebaseAdvice)
		}

		if strings.Join(headNonArtLines, "\n") != strings.Join(refNonArtLines, "\n") {
			return PushResult{}, fmt.Errorf("ahead commits have unrelated changes in %s; %s", refIndexPath, pullRebaseAdvice)
		}

		if !headHasIndex {
			fullIndexPath := filepath.Join(busDir, filepath.FromSlash(refIndexPath))
			needAppend := true
			if diskIndex, err := os.ReadFile(fullIndexPath); err == nil {
				for _, l := range strings.Split(string(diskIndex), "\n") {
					l = strings.TrimSpace(l)
					if l == "" {
						continue
					}
					f := strings.Split(l, "\t")
					if len(f) > 0 && f[0] == art.ID {
						if l == wantIndexLine {
							needAppend = false
							break
						}
						return PushResult{}, fmt.Errorf("local index in %s has conflicting record for %s", refIndexPath, art.ID)
					}
				}
			}
			if needAppend {
				if err := p.AppendIndex(busDir); err != nil {
					return PushResult{}, err
				}
			}
			wroteAttrs, err := EnsureMergeAttributes(busDir)
			if err != nil {
				return PushResult{}, err
			}
			pathsToStage := []string{refIndexPath}
			if wroteAttrs {
				pathsToStage = append(pathsToStage, AttributesName)
			}
			id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
			if _, err := git(busDir, append([]string{"add"}, pathsToStage...)...); err != nil {
				return PushResult{}, err
			}
			if ahead == 1 {
				if _, err := git(busDir, append(identityArgs(id), "commit", "--amend", "--no-edit")...); err != nil {
					return PushResult{}, err
				}
			} else {
				msg := WithTrailer(p.Message, wantTrailer)
				if _, err := git(busDir, append(identityArgs(id), "commit", "-m", msg)...); err != nil {
					return PushResult{}, err
				}
			}
		}

		headSha, err := HeadCommit(busDir)
		if err != nil {
			return PushResult{}, err
		}
		reusedCommit = headSha
	}

	if reusedCommit == "" {
		// Reconcile note on disk
		fullNotePath := filepath.Join(busDir, filepath.FromSlash(art.Path))
		if _, err := os.Stat(fullNotePath); err == nil {
			diskBytes, err := os.ReadFile(fullNotePath)
			if err != nil {
				return PushResult{}, err
			}
			if string(diskBytes) != art.Note {
				return PushResult{}, fmt.Errorf("local file %s has conflicting content", art.Path)
			}
		} else {
			if err := p.Save(busDir); err != nil {
				return PushResult{}, err
			}
		}

		// Reconcile index on disk
		fullIndexPath := filepath.Join(busDir, filepath.FromSlash(IndexPath(p.Sender.Lane)))
		wantIndexLine := IndexLine(p.Index)
		needAppend := true
		if diskIndex, err := os.ReadFile(fullIndexPath); err == nil {
			for _, l := range strings.Split(string(diskIndex), "\n") {
				if strings.TrimSpace(l) == "" {
					continue
				}
				f := strings.Split(l, "\t")
				if len(f) > 0 && f[0] == art.ID {
					if l == wantIndexLine {
						needAppend = false
						break
					}
					return PushResult{}, fmt.Errorf("local index in %s has conflicting record for %s", IndexPath(p.Sender.Lane), art.ID)
				}
			}
		}
		if needAppend {
			if err := p.AppendIndex(busDir); err != nil {
				return PushResult{}, err
			}
		}

		wroteAttrs, err := EnsureMergeAttributes(busDir)
		if err != nil {
			return PushResult{}, err
		}
		paths := p.Paths()
		if wroteAttrs {
			paths = append(paths, AttributesName)
		}

		id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
		msg := WithTrailer(p.Message, wantTrailer)
		cSha, err := stageAndCommit(busDir, id, paths, msg)
		if err != nil {
			return PushResult{}, err
		}
		reusedCommit = cSha
	}

	// Step 5: Push loop with bounded race handling
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	var res PushResult
	res.Commit = reusedCommit
	for attempt := 1; attempt <= attempts; attempt++ {
		res.Attempts = attempt
		if _, err := git(busDir, "push", remote, "HEAD:refs/heads/"+branch); err == nil {
			headSha, _ := HeadCommit(busDir)
			res.Commit = headSha
			res.Pushed = true
			res.State = "published"
			return res, nil
		}
		if attempt == attempts {
			break
		}
		sleepBetweenAttempts(pushBackoff(attempt))
		if _, err := git(busDir, "fetch", remote, branch); err != nil {
			return PushResult{}, fmt.Errorf("the push was refused and the fetch that would explain it failed: %w", err)
		}
		ref = trackingRef(busDir, remote, branch)
		if pub, cSha, _ := queryRemote(busDir, ref, p, art); pub {
			res.Commit = cSha
			res.Pushed = true
			res.State = "published"
			return res, nil
		}
		if _, err := git(busDir, append(identityArgs(id), "rebase", "FETCH_HEAD")...); err != nil {
			if settleErr := settleRebase(busDir, id); settleErr != nil {
				if abortErr := abortRebase(busDir); abortErr != nil {
					return PushResult{}, abortErr
				}
				return PushResult{}, &ConflictError{
					Reason: fmt.Sprintf("the rebase over what arrived conflicted on %s, which this tool will not settle for you; your commit %s is on the branch and was NOT pushed; recover with `git pull --rebase`, fix the files it names, `git rebase --continue`, then `git push`",
						oneLineOf(settleErr.Error()), res.Commit),
					Output: transcriptOf(err),
				}
			}
		}
		headSha, err := HeadCommit(busDir)
		if err != nil {
			return PushResult{}, err
		}
		res.Commit = headSha
	}
	return res, fmt.Errorf("the push was refused %d times; your commit %s is on the branch and was NOT pushed; the next run of this tool will carry it, or land it now with `git pull --rebase && git push`", res.Attempts, res.Commit)
}
