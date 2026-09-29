package definition

import (
	"context"
	"fmt"
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// AdmissionSchema names the shape of the canonical admission record.
const AdmissionSchema = "card-admission/1"

// Admission is the record one admitted card carries: identities, digests and the
// header fields that are data. It holds no brief, no DONE-WHEN and no PROBES:
// their bytes are covered by Digest, and the brief alone by BriefDigest. Its
// shared fields (ID, Digest, ObjectID, Commit, Repository, Path, Kind, DependsOn,
// Entry, Title) are those of the request package's admission, under the same names
// and the same grammar, so a record is what an admission request carries plus the
// stream row and the review policy's identity.
type Admission struct {
	ID            string
	Digest        string // hex SHA-256 of the card file: the definition digest
	BriefDigest   string // hex SHA-256 of the brief
	ObjectID      string // the Git object id of the committed blob
	Commit        string // the full commit the blob was read at
	Repository    card.Repository
	Path          string // the repository-relative path of the card file
	Kind          string
	Completion    Completion
	PolicyVersion int      // the completion policy the kind was classified under
	DependsOn     []string // sorted, unique, at most MaxDependsOn, never the card itself
	Entry         string   // "" when the card has none
	Tier          string
	CardSchema    string // the card's SCHEMA value
	Title         string
	Paths         []string // sorted
	Test          string   // the TEST value as the card writes it
	Doors         string
	BaseCommit    string // the contract line's sha=: the commit the work starts from; "" when absent
}

// Admissions is the one function from a repository to admission records: it pins
// the committed blobs of the paths at the commit (never reading a working file and
// never the network), parses the pinned bytes itself, validates the array and
// produces one record per card, in the order of the paths. It never takes a
// definition or a digest from its caller, so a record is always what the committed
// bytes say. It returns no records beside a refusal: a refused array is refused
// whole, at the first stage that refused (pin, then parse, then validate), and it
// reports every refusal of that stage (at most card.MaxRefusals, the rest counted).
//
// repoDir is the root of a git repository, commit a full lower-case object id and
// paths the repository-relative paths of the card files; see pinDir for what is
// refused and what is accepted.
func Admissions(ctx context.Context, repoDir, commit string, paths []string, opts ...PinOption) ([]Admission, *Refusals) {
	pins, r := pinDir(ctx, repoDir, commit, paths, opts...)
	if r != nil {
		return nil, r
	}
	return fromPins(pins)
}

// fromPins parses pinned bytes, validates the array and builds the records.
func fromPins(pins []pinned) ([]Admission, *Refusals) {
	defs, r := parse(sources(pins))
	if r != nil {
		return nil, r
	}
	if _, r := validate(defs); r != nil {
		return nil, r
	}
	out := make([]Admission, len(defs))
	var c card.Collector
	for i, d := range defs {
		p := pins[i]
		cs, _ := Classify([]string{d.Kind})
		deps := append([]string(nil), d.DependsOn...)
		sort.Strings(deps)
		paths := append([]string(nil), d.Paths...)
		sort.Strings(paths)
		out[i] = Admission{
			ID: d.ID, Digest: d.Digest, BriefDigest: d.BriefDigest, ObjectID: p.ObjectID, Commit: p.Commit, Repository: p.Repository, Path: p.Path,
			Kind: d.Kind, Completion: cs[0], PolicyVersion: PolicyVersion(), DependsOn: deps, Entry: d.Entry, Tier: d.Tier, CardSchema: d.Schema,
			Title: d.Title, Paths: paths, Test: d.Test.String(), Doors: d.Doors, BaseCommit: d.BaseCommit,
		}
		if b, _ := encodeOne(out[i]); len(b) > card.MaxAdmissionRecordBytes {
			c.Add(ref(OpAdmit, CauseTooLarge, p.Path, 0, "", fmt.Sprintf("a record of %d bytes", len(b)), fmt.Sprintf("%d bytes", card.MaxAdmissionRecordBytes),
				"shorten the card's header values"))
		}
	}
	if err := c.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (a Admission) tree() card.Obj {
	m := card.Obj{}
	m.Str("schema", AdmissionSchema)
	m.Str("card_schema", a.CardSchema)
	m.Str("id", a.ID)
	m.Str("digest", a.Digest)
	m.Str("brief_digest", a.BriefDigest)
	m.Str("object_id", a.ObjectID)
	m.Str("commit", a.Commit)
	m.Str("repository", string(a.Repository))
	m.Str("path", a.Path)
	m.Str("kind", a.Kind)
	m.Str("completion", string(a.Completion))
	m.Str("completion_policy", fmt.Sprint(a.PolicyVersion))
	m.OptSet("depends_on", card.Strings(a.DependsOn))
	m.Str("entry", a.Entry)
	m.Str("tier", a.Tier)
	m.Str("title", a.Title)
	m.OptSet("paths", card.Strings(a.Paths))
	m.Str("test", a.Test)
	m.Str("doors", a.Doors)
	m.Str("base_commit", a.BaseCommit)
	return m
}

func encodeOne(a Admission) ([]byte, *Refusals) { return card.Encode(a.tree()), nil }

// EncodeAdmissions writes each record in its canonical form (card.Encode): one
// JSON object, keys in byte order, only strings and arrays of strings (no numbers,
// so no floats), no insignificant whitespace, no HTML escaping, the arrays sorted
// by the encoder, an empty optional field left out. The same record always
// encodes to the same bytes.
func EncodeAdmissions(as []Admission) [][]byte {
	out := make([][]byte, len(as))
	for i, a := range as {
		out[i], _ = encodeOne(a)
	}
	return out
}

// DigestAdmissions is the hex SHA-256 of each record's canonical encoding.
func DigestAdmissions(as []Admission) []string {
	enc := EncodeAdmissions(as)
	out := make([]string, len(enc))
	for i, b := range enc {
		out[i] = string(card.Sum(b))
	}
	return out
}
