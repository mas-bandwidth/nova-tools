package wake

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ReportName is the one file name this source watches, at any depth under each
// --reports directory. One agreed name, because a watcher that woke on every
// file another line touched would wake on its own scratch.
const ReportName = "RESULT.md"

// Reports is the report-file source. There are NO default report directories:
// the prototype hardcoded $HOME/deepseek-working-*/jobs/*/RESULT.md and
// $HOME/freddy-working-*/jobs/*/, which is three guessed paths, a guessed $HOME
// and two other lines' layouts frozen into a tool.
type Reports struct {
	Dirs   []string
	Every_ time.Duration
}

func (r *Reports) Name() string         { return "reports" }
func (r *Reports) Every() time.Duration { return r.Every_ }

// Poll walks each directory. A file that DISAPPEARS is not a change and its key
// is kept: a job directory being rebuilt is not news, and the window does not
// want to be woken by an rm.
//
// The state value is mtime:size, and the known limit is named rather than
// discovered: mtime:size cannot see a rewrite that preserves both, and some
// tools write identical-length updates within one second. A content digest
// would close it and costs a read of every watched file on every poll; it is a
// v2 item behind a flag, and until it exists this paragraph is the answer to
// "why did it not wake".
func (r *Reports) Poll(ctx context.Context, now time.Time) (Result, error) {
	res := Result{}
	bad := 0
	var lastErr error
	for _, dir := range r.Dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				// One unreadable directory INSIDE a readable root is not the
				// root being unreadable: skip it and keep walking.
				if path == dir {
					return err
				}
				return nil
			}
			if d.IsDir() || d.Name() != ReportName {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			res.Items = append(res.Items, Item{
				Kind:  KindReport,
				Key:   "report:" + path,
				Value: Compose(strconv.FormatInt(info.ModTime().UnixNano(), 10), strconv.FormatInt(info.Size(), 10), strconv.Itoa(countLines(path)), ""),
			})
			return nil
		})
		if err != nil {
			bad++
			lastErr = err
		}
	}
	if len(r.Dirs) > 0 && bad == len(r.Dirs) {
		return res, fmt.Errorf("every --reports directory is unreadable: %s", oneLineOf(lastErr.Error()))
	}
	return res, nil
}

// countLines is read at report time and is on the WAKE REPORT line so the
// window can tell a stub from a finding without opening it. A file that cannot
// be read counts zero rather than failing the poll: the path and the size are
// the news, and this number is a courtesy.
func countLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := bytes.Count(raw, []byte("\n"))
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		n++
	}
	return n
}

// NewOrModified stamps an observed report value with the change kind, which is
// new when there is no stored value and modified when there is a different one.
// The kind is in the VALUE so that a record the cap elided renders the same
// sentence when the next call prints it out of the queue.
// ReportIdentity is what a report file is COMPARED by: mtime:size, and nothing
// else. The known limit is named in the spec rather than discovered -- mtime:size
// cannot see a rewrite that preserves both -- and a limit the spec pins as
// behaviour is not one the code may quietly close by folding the line count into
// the compared value: a digest costs a read of every watched file on every poll
// and is a v2 item behind a flag.
func ReportIdentity(value string) string {
	p := fields(Decompose(value), 4)
	return Compose(p[0], p[1])
}

func NewOrModified(value string, had bool) string {
	p := fields(Decompose(value), 4)
	if had {
		p[3] = "modified"
	} else {
		p[3] = "new"
	}
	return Compose(p...)
}

// LineIsOffline reads a --line state value's state field. It is here, beside
// the value's other readers, so that the cold-start rule asks one function
// rather than matching a substring of a composed value.
func LineIsOffline(value string) bool {
	p := fields(Decompose(value), 3)
	return p[1] == "OFFLINE"
}
