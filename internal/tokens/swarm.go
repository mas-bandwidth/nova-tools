package tokens

import (
	"bufio"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// --swarm <label>=<pool>: nova-worker's usage files.
//
// The swarm writes one usage file per job under <pool>/usage/, OUTSIDE the directory
// `reclaim` removes and before the job's files move. That is the whole reason this is a
// source: a job whose per-job data home is reclaimed with the job would otherwise go
// unaccounted, and a sidecar written before the move is counted after the directory is
// gone.
//
// Nothing under done/, failed/ or running/ is OPENED. A job directory there with no usage
// file is counted by its NAME alone, so the answer is the same before and after reclaim.

// SwarmColumns are the sixteen usage-file columns, in the order the swarm writes them:
// `job`, `attempt`, `from`, `started`, `ended`, `end`, `rc`, `provider`, `model`, `repo`,
// `tokens_in`, `tokens_out`, `cache_write`, `cache_read`, `reasoning`, `usd`. A header
// that is not these is refused by name. The reader looks every cell up BY NAME, so this
// list is the whole contract with the swarm.
var SwarmColumns = []string{
	"job", "attempt", "from", "started", "ended", "end", "rc", "provider",
	"model", "repo", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// swarmTypes maps the usage row's own column names onto the five types.
var swarmTypes = map[string]Type{
	"tokens_in": Input, "tokens_out": Output,
	"cache_write": CacheWrite, "cache_read": CacheRead, "reasoning": Reasoning,
}

// ReadSwarm reads every <pool>/usage/*.tsv and nothing else. The pool is read through the
// caller's fs.FS, rooted at pool (os.DirFS(pool) in main, fstest.MapFS in a test), and
// pool is the path the report names.
func ReadSwarm(label, pool string, fsys fs.FS, rules *Rules) *Source {
	s := &Source{Label: Label(KindSwarm, label), Kind: KindSwarm, Path: pool, Reports: AllTypes, Basis: UTC}

	usageDir := filepath.Join(pool, "usage")
	ents, err := fs.ReadDir(fsys, "usage")
	if err != nil {
		s.unreadable(usageDir, err.Error())
	}
	var files []string
	have := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileSuffix) {
			continue
		}
		files = append(files, filepath.Join("usage", e.Name()))
		have[strings.TrimSuffix(e.Name(), FileSuffix)] = true
	}
	sort.Strings(files)

	seen := map[string]bool{}
	for _, name := range files {
		path := filepath.Join(pool, name)
		s.Stat.Files++
		f, err := openSourceFS(fsys, name)
		if err != nil {
			s.unreadable(path, err.Error())
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		n := 0
		var cols map[string]int
		bad := false
		for sc.Scan() {
			n++
			line := strings.TrimRight(sc.Text(), "\r")
			if line == "" {
				continue
			}
			cells := strings.Split(line, "\t")
			// The header is the first NON-BLANK line, not line 1: a file with a leading
			// blank line never validated its header at all, so every row after it was
			// read against a nil column map -- every lookup column 0 -- and the file
			// never got the named wrong-column refusal the spec promises.
			if cols == nil {
				if wrong := wrongColumn(cells); wrong != "" {
					s.unparsed(path, n, wrong)
					bad = true
					break
				}
				cols = map[string]int{}
				for i, c := range cells {
					cols[c] = i
				}
				continue
			}
			if len(cells) != len(SwarmColumns) {
				s.unparsed(path, n, strconv.Itoa(len(cells))+" columns, want "+strconv.Itoa(len(SwarmColumns)))
				continue
			}
			job := cells[cols["job"]]
			if job == "" {
				s.Stat.NoID++
				continue
			}
			// One row per (job, attempt): a failed attempt followed by a retry
			// retains both costs, because a row for a second attempt is its own
			// row. Only a repeated row for the same attempt is a duplicate,
			// never a retry.
			if seen[job+"\x00"+cells[cols["attempt"]]] {
				s.Stat.Dup++
				continue
			}
			seen[job+"\x00"+cells[cols["attempt"]]] = true
			day, okDay := DayOfStamp(cells[cols["ended"]])
			if !okDay {
				s.unparsed(path, n, "the ended stamp is not a date this tool can read: "+cells[cols["ended"]])
				continue
			}
			rules.SetDay(day)
			m := Message{
				Day:   day,
				Basis: UTC,
				Model: cells[cols["model"]],
				// The swarm already attributed the job and its paths are gone with the
				// directory, so the recorded name is the only token, through the one
				// attribution function: a name the rules file does not know is `other`,
				// never a seventh bucket.
				Repo: rules.Attribute([]string{cells[cols["repo"]]}, ""),
			}
			for name, t := range swarmTypes {
				cell := cells[cols[name]]
				if cell == Dash || cell == "" {
					continue
				}
				v, err := strconv.ParseInt(cell, 10, 64)
				if err != nil {
					continue
				}
				m.Counts.Set(t, v)
			}
			if p := cells[cols["provider"]]; p != "" && p != Dash {
				m.Provider = p
			}
			if u := cells[cols["usd"]]; u != "" && u != Dash {
				if micro, ok := ParseMicro(u); ok {
					m.Usd, m.Priced = micro, true
				}
			}
			s.Stream = append(s.Stream, m)
			s.Stat.Messages++
		}
		_ = f.Close() // ignored: the file is opened only for reading
		if bad {
			continue
		}
		if err := sc.Err(); err != nil {
			s.unreadable(path, err.Error())
		}
	}

	// nousage: a job directory under done/ or failed/ with no usage file beside it.
	// Counted by NAME; nothing under those directories is opened for anything.
	for _, sub := range []string{"done", "failed"} {
		ents, err := fs.ReadDir(fsys, sub)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() && !have[e.Name()] {
				s.Stat.NoUsage++
			}
		}
	}
	return s
}

// wrongColumn names the first column that is not the sixteen in order, or the empty string
// when the header is right.
func wrongColumn(cells []string) string {
	for i, want := range SwarmColumns {
		if i >= len(cells) {
			return "the header is missing the column " + want + " (SPEC-WORKER rule 12 names sixteen, in order)"
		}
		if cells[i] != want {
			return "column " + strconv.Itoa(i+1) + " is " + cells[i] + ", want " + want + " (SPEC-WORKER rule 12 names sixteen, in order)"
		}
	}
	if len(cells) > len(SwarmColumns) {
		return "the header has " + strconv.Itoa(len(cells)) + " columns, want " + strconv.Itoa(len(SwarmColumns))
	}
	return ""
}

// unparsed records a line, a note or a header this run could not read.
func (s *Source) unparsed(note string, line int, text string) {
	s.Stat.Unparsed++
	s.Unparseds = append(s.Unparseds, Unparsed{Label: s.Label, Note: note, Line: line, Text: text})
}
