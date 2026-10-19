package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/roadmap"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// roadmapVerbs edit and read a repository's own work record, docs/roadmap.sexp
// and docs/fixes.sexp (internal/roadmap; tla/RoadmapEntry.tla), and render
// ROADMAP.md and FIXES.md in the same step, so no one edits the data or the
// pages by hand. They are the verbs of nova-sprint 8c87a4b (check, add, remove,
// pull, note), which edited the private work record's roadmaps/*.sexp and were
// left out by the v1.2.3 re-seed, pointed at the repository's own files, with
// done, list and render added. Every verb reads and writes local files only:
// no GitHub call per entry, and a batch of edits lands as one commit.
func (g github) roadmapVerbs() []tool.Verb {
	return []tool.Verb{
		{
			Name:      "roadmap check",
			Usage:     "roadmap check [--repo <dir>]",
			Example:   "roadmap check",
			Effect:    tool.Inspection,
			Detail:    roadmapDetail,
			ExitTable: "0 both files decode, no id stands in both, and each page is what its data renders; 1 check found problems, one FAILED line each; 2 could not run (a flag, an unreadable file)",
			Flags:     repoFlag,
			Run:       roadmapCheck,
		},
		{
			Name:      "roadmap list",
			Usage:     "roadmap list [--repo <dir>] [--file roadmap|fixes] [--release <v> | --group <g>] [--state <s>] [--text <t>] [--max <n>]",
			Example:   "roadmap list --release v1.2.6 --state planned",
			Effect:    tool.Inspection,
			Detail:    roadmapDetail,
			ExitTable: "0 listed (none is a list too); 2 could not run (a flag, a file that does not decode)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				f.String("file", "", "list only one file: roadmap (docs/roadmap.sexp) or fixes (docs/fixes.sexp)")
				f.String("release", "", "list only the fixes of this release, and the scheduled notes for it")
				f.String("group", "", "list only the items of this roadmap group")
				f.String("state", "", "list only entries in this state: todo, scheduled, done, planned, in-progress or shipped")
				f.String("text", "", "list only entries whose id, title or text holds these words (any case)")
				f.Max()
				f.Check(func(c *tool.Call) {
					if v := c.Str("file"); v != "" && v != "roadmap" && v != "fixes" {
						c.Problem(fmt.Sprintf("--file %q wants roadmap or fixes", v))
					}
					if c.Str("release") != "" && c.Str("group") != "" {
						c.Problem("--release and --group name two places; give one")
					}
				})
			},
			Run: roadmapList,
		},
		{
			Name:      "roadmap add",
			Usage:     "roadmap add [--repo <dir>] --id <id> (--release <v> | --group <g>) --title <t> [--text <t>] --origin <o> [--status planned|in-progress] [--why <w>]",
			Example:   `roadmap add --id my-fix --release v1.2.6 --title "What the fix does" --origin "PR #12"`,
			Effect:    tool.LocalWrite + ": rewrites docs/roadmap.sexp or docs/fixes.sexp and its page",
			Detail:    roadmapDetail,
			ExitTable: "0 the entry is added and the data and its page saved; 2 could not run, nothing written (a flag, a duplicate id, a group or release not there, a shipped release, data that would not decode)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				f.Required("id", "the new entry's id: lower case, digits, dots and dashes, unique across both files")
				placeFlags(f, "add the entry to")
				f.Required("title", "the entry's title, one line")
				f.String("text", "", "what the entry is (required for a roadmap item)")
				f.String("origin", "", "where the entry came from: PR #123, 40-char SHA, or commit reference (required for a fix)")
				f.String("status", "planned", "a new fix's status: planned or in-progress")
				f.String("why", "", "a roadmap item's reason to wait")
				f.Check(func(c *tool.Call) {
					if c.Str("group") != "" && strings.TrimSpace(c.Str("text")) == "" {
						c.Problem("--text is required for a roadmap item; it wants what the item is")
					}
					if c.Str("release") != "" && strings.TrimSpace(c.Str("origin")) == "" {
						c.Problem("--origin is required for a fix; it wants where the fix came from (valid origin format examples: PR #123, 40-char SHA, or commit reference)")
					}
				})
			},
			Run: g.roadmapAdd,
		},
		{
			Name:      "roadmap remove",
			Usage:     "roadmap remove [--repo <dir>] --id <id>...",
			Example:   "roadmap remove --id my-old-fix",
			Effect:    tool.LocalWrite + ": rewrites docs/roadmap.sexp or docs/fixes.sexp and its page",
			Detail:    roadmapDetail,
			ExitTable: "0 the entries are removed and the data and pages saved; 2 could not run, nothing written (a flag, an unknown id, a done or shipped entry, the last entry of its group or release, a shipped release)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				idFlag(f, "remove")
			},
			Run: g.roadmapRemove,
		},
		{
			Name:      "roadmap pull",
			Usage:     "roadmap pull [--repo <dir>] --id <id>... (--release <v> | --group <g>)",
			Example:   "roadmap pull --id my-item --release v1.2.6",
			Effect:    tool.LocalWrite + ": rewrites docs/roadmap.sexp and docs/fixes.sexp and their pages",
			Detail:    roadmapDetail,
			ExitTable: "0 the entries are moved and the data and pages saved; 2 could not run, nothing written (a flag, an unknown id, a done or shipped entry, the last entry of its group or release, a group or release not there, a shipped release)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				idFlag(f, "move")
				placeFlags(f, "move the entries to")
			},
			Run: g.roadmapPull,
		},
		{
			Name:      "roadmap done",
			Usage:     "roadmap done [--repo <dir>] --id <id>... --evidence <PR #n | commit>",
			Example:   `roadmap done --id my-fix --evidence "PR #12"`,
			Effect:    tool.LocalWrite + ": rewrites docs/roadmap.sexp or docs/fixes.sexp and its page",
			Detail:    roadmapDetail,
			ExitTable: "0 the entries are done and the data and pages saved; 2 could not run, nothing written (a flag, an unknown id, evidence that is no pull request or commit, an entry already done or shipped, the last item of its group, a shipped release)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				idFlag(f, "mark done")
				f.Required("evidence", "what shows it is done: a pull request (PR #12 or #12), a commit (7 to 40 hex digits), or a github.com pull or commit link")
			},
			Run: g.roadmapDone,
		},
		{
			Name:      "roadmap note",
			Usage:     "roadmap note [--repo <dir>] --id <id>... [--text <t>] [--title <t>]",
			Example:   `roadmap note --id my-fix --text "Needs the store migration first."`,
			Effect:    tool.LocalWrite + ": rewrites docs/roadmap.sexp or docs/fixes.sexp and its page",
			Detail:    roadmapDetail,
			ExitTable: "0 the text is appended (and the title given) and the data and pages saved; 2 could not run, nothing written (a flag, an unknown id)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				idFlag(f, "note")
				f.String("text", "", "the words to append to each entry's :text")
				f.String("title", "", "a new title for each entry; the title it had is kept under :kept (earlier-title)")
				f.Check(func(c *tool.Call) {
					if strings.TrimSpace(c.Str("text")) == "" && strings.TrimSpace(c.Str("title")) == "" {
						c.Problem("--text or --title is required; note wants words to append, a new title, or both")
					}
				})
			},
			Run: g.roadmapNote,
		},
		{
			Name:      "roadmap render",
			Usage:     "roadmap render [--repo <dir>]\nroadmap render --file <data.sexp> --out <page.md> [--source <name>]",
			Example:   "roadmap render",
			Effect:    tool.LocalWrite + ": writes ROADMAP.md and FIXES.md, or --out",
			Detail:    roadmapDetail,
			ExitTable: "0 the pages are written (or were already what the data renders); 2 could not run, nothing written (a flag, data that does not decode, an unwritable page)",
			Flags: func(f *tool.Flags) {
				repoFlag(f)
				f.String("file", "", "render this data `file` instead of the repository's own: a (roadmap \"v1\" ...) or (fixes \"v1\" ...) form held anywhere")
				f.String("out", "", "the page `file` --file renders to (required with --file)")
				f.String("source", "", "the data's name in the page's header (default: --file as given)")
				f.Check(func(c *tool.Call) {
					if (c.Str("file") == "") != (c.Str("out") == "") {
						c.Problem("--file and --out go together: the data and the page it renders to")
					}
					if c.Str("file") != "" && c.Given("repo") {
						c.Problem("--repo renders the repository's own files and --file renders one held elsewhere; give one")
					}
				})
			},
			Run: roadmapRender,
		},
	}
}

const roadmapDetail = `A repository's work record is two files of data and the two pages they render:
docs/roadmap.sexp -> ROADMAP.md (groups of items: work after the release
ladder) and docs/fixes.sexp -> FIXES.md (point releases and their fixes). The
shapes are nova-tools pkg/roadmap's. Every writing verb edits the data, decodes it
again, and writes the data and its page together; nothing is written when any
check fails, and every byte it does not change (comments, spacing, the other
records) is kept. Nothing reaches GitHub: commit the batch of edits as one.

An entry is todo (a roadmap item), open (a fix, planned or in-progress), or
done (an item on the roadmap's :done list, a fix whose status is done or
shipped). Done is final. A shipped release is frozen. Every group and release
keeps one entry, and every entry names a group or release that is there
(tla/RoadmapEntry.tla).

check:  the files decode, no id stands in both, each page is what its data renders.
list:   entries by file, release or group, state and words; --json for a program.
add:    an item under --group, or a fix in --release (planned unless --status).
remove: the entries go.
pull:   to --release, an item becomes a fix (planned) or a fix changes release;
        to --group, a fix becomes an item or an item changes group.
done:   a fix's status becomes done, an item moves to :done; --evidence is
        appended to the text.
note:   --text is appended to each entry's text; --title retitles it, the old
        title kept under :kept.
A pull across the files, and done of an item, carry every field: one the
target has no key for is kept under :kept (item-why on a fix, fix-status on
an item), and a move back restores it.
render: writes the pages from the data; --file/--out renders data kept
        elsewhere (a roadmap held in the private work repository) to a page.

The old nova-work roadmap verbs edited the private work record's
roadmaps/*.sexp ((:roadmap ... :releases ...) with streams of cards). The
v1.2.3 re-seed left them out; these verbs replace them, on the repository's own
docs/roadmap.sexp and docs/fixes.sexp.`

// repoFlag declares --repo: the repository root whose work record the verb reads.
func repoFlag(f *tool.Flags) {
	f.String("repo", ".", "the repository root holding docs/roadmap.sexp and docs/fixes.sexp (default .)")
}

// placeFlags declares --release and --group, one of which is required.
func placeFlags(f *tool.Flags, what string) {
	f.String("release", "", what+" this fixes release (docs/fixes.sexp)")
	f.String("group", "", what+" this roadmap group (docs/roadmap.sexp)")
	f.Check(func(c *tool.Call) {
		switch r, g := c.Str("release"), c.Str("group"); {
		case r == "" && g == "":
			c.Problem("--release or --group is required; it wants where the entry goes: a fixes release or a roadmap group")
		case r != "" && g != "":
			c.Problem("--release and --group name two places; give one")
		}
	})
}

// idFlag declares --id, repeated: the entries a verb acts on.
func idFlag(f *tool.Flags, verb string) {
	f.Var(&idList{}, "id", "an entry `id` to "+verb+"; repeat for more (required)")
	f.Check(func(c *tool.Call) {
		if len(ids(c)) == 0 {
			c.Problem("--id is required; it wants an entry id, repeated for more; refusing to guess")
		}
	})
}

// idList is --id, repeated.
type idList []string

func (l *idList) String() string { return strings.Join(*l, ",") }
func (l *idList) Get() any       { return []string(*l) }
func (l *idList) Set(v string) error {
	if strings.TrimSpace(v) == "" || strings.ContainsAny(v, " \t\n\"") {
		return fmt.Errorf("%q is not an entry id", v)
	}
	*l = append(*l, v)
	return nil
}

func ids(c *tool.Call) []string { return c.Get("id").([]string) }

// refuse is a refusal whose remedy is the verb's own help.
func refuse(verb, why string) *tool.Out {
	o := tool.Refuse(oneLine(why))
	o.Remedy = "nova-work roadmap " + verb + " -h"
	return o
}

func oneLine(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", "; ") }

func (g github) today() string { return g.now().Format("2006-01-02") }

// edit loads the work record, applies one edit, and saves the data and pages;
// a refused edit writes nothing.
func (g github) edit(c *tool.Call, verb string, apply func(s *roadmap.Set) error) *tool.Out {
	s, err := roadmap.Load(c.Str("repo"), g.today())
	if err != nil {
		o := refuse(verb, err.Error()+"; nothing written")
		o.Remedy = "nova-work roadmap check --repo " + c.Str("repo")
		return o
	}
	if err := apply(s); err != nil {
		return refuse(verb, err.Error()+"; nothing written")
	}
	if err := s.Save(); err != nil {
		return refuse(verb, "save: "+err.Error())
	}
	o := tool.Done().Fact("repo", c.Str("repo"))
	for _, f := range s.Changed() {
		o.Item("wrote", "file", f)
	}
	return o
}

func roadmapCheck(c *tool.Call) *tool.Out {
	probs, err := roadmap.Check(c.Str("repo"))
	if err != nil {
		return refuse("check", err.Error())
	}
	if len(probs) > 0 {
		return tool.Fail(probs...).Fact("repo", c.Str("repo")).Fact("problems", len(probs))
	}
	s, err := roadmap.Load(c.Str("repo"), "")
	if err != nil {
		return refuse("check", err.Error())
	}
	return tool.Done().Fact("repo", c.Str("repo")).Fact("entries", len(s.Entries()))
}

func roadmapList(c *tool.Call) *tool.Out {
	s, err := roadmap.Load(c.Str("repo"), "")
	if err != nil {
		o := refuse("list", err.Error())
		o.Remedy = "nova-work roadmap check --repo " + c.Str("repo")
		return o
	}
	found := s.Find(roadmap.Query{File: c.Str("file"), Place: c.Str("release") + c.Str("group"), State: c.Str("state"), Text: c.Str("text")})
	o := tool.Done().Fact("repo", c.Str("repo")).Fact("entries", len(found))
	for _, e := range found {
		o.Item("entry", "id", e.ID, "file", e.File, "kind", e.Kind, "place", e.Place, "state", e.State, "title", e.Title)
	}
	return o
}

func (g github) roadmapAdd(c *tool.Call) *tool.Out {
	o := g.edit(c, "add", func(s *roadmap.Set) error {
		if c.Str("group") != "" {
			return s.AddItem(roadmap.Item{ID: c.Str("id"), Group: c.Str("group"), Title: c.Str("title"), Text: c.Str("text"), Origin: c.Str("origin"), Why: c.Str("why")})
		}
		return s.AddFix(roadmap.Fix{ID: c.Str("id"), Release: c.Str("release"), Title: c.Str("title"), Text: c.Str("text"), Origin: c.Str("origin"), Status: c.Str("status")})
	})
	if o.Status == tool.OK {
		o.Fact("id", c.Str("id")).Fact("place", c.Str("release")+c.Str("group"))
	}
	return o
}

func (g github) roadmapRemove(c *tool.Call) *tool.Out {
	o := g.edit(c, "remove", func(s *roadmap.Set) error { return s.Remove(ids(c)...) })
	if o.Status == tool.OK {
		o.Fact("removed", len(ids(c)))
	}
	return o
}

func (g github) roadmapPull(c *tool.Call) *tool.Out {
	o := g.edit(c, "pull", func(s *roadmap.Set) error { return s.Pull(c.Str("release"), c.Str("group"), ids(c)...) })
	if o.Status == tool.OK {
		o.Fact("moved", len(ids(c))).Fact("place", c.Str("release")+c.Str("group"))
	}
	return o
}

func (g github) roadmapDone(c *tool.Call) *tool.Out {
	o := g.edit(c, "done", func(s *roadmap.Set) error { return s.MarkDone(c.Str("evidence"), ids(c)...) })
	if o.Status == tool.OK {
		o.Fact("done", len(ids(c))).Fact("evidence", c.Str("evidence"))
	}
	return o
}

func (g github) roadmapNote(c *tool.Call) *tool.Out {
	o := g.edit(c, "note", func(s *roadmap.Set) error { return s.Note(c.Str("text"), c.Str("title"), ids(c)...) })
	if o.Status == tool.OK {
		o.Fact("noted", len(ids(c)))
	}
	return o
}

func roadmapRender(c *tool.Call) *tool.Out {
	if in := c.Str("file"); in != "" {
		source := c.Str("source")
		if source == "" {
			source = filepath.ToSlash(in)
		}
		kind, items, err := roadmap.RenderFile(in, c.Str("out"), source)
		if err != nil {
			return refuse("render", err.Error())
		}
		return tool.Done().Fact("kind", kind).Fact("items", items).Fact("out", c.Str("out"))
	}
	changed, err := roadmap.Render(c.Str("repo"))
	if err != nil {
		return refuse("render", err.Error())
	}
	o := tool.Done().Fact("repo", c.Str("repo")).Fact("changed", len(changed))
	for _, p := range changed {
		o.Item("wrote", "file", p)
	}
	return o
}
