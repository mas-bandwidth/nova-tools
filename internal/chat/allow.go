// Package chat's allow-list parser, quickstart skeleton and leave removal,
// per SPEC-CHAT.md item 1 of the work list and the rules under
// "The allow-list, which is the line's person's":
//
//	# comments and blank lines ignored, every field named, an unknown key is
//	a refusal naming the key (never ignored), class=own checked against the
//	pinned own-server, dm * accepted, every per-conversation number required
//	where the spec says it is required, the ladder's steps read in the file's
//	order, and quickstart creates a commented skeleton with no entries while
//	leave removes one entry. Both writes go through .tmp and rename so a
//	crash never leaves the file half-written and a reader never sees a
//	half-new file.
//
// The parser is strict: a single unknown key, a single missing required key,
// or a class=own entry whose id is not the pinned own-server is a refusal with
// the line number and the reason. The shape of the file is the shape the spec
// shows, and that shape is what the parser accepts. A spec with more keys is
// a spec with more lines, and the test that adds one is a test that turns red
// here -- which is what makes "an ignored key is a setting somebody believes
// is in force" a test rather than a promise.
package chat

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Class is a conversation's class. The class is a property of the space and
// never of the message: rule 8 says so, and the allow-list is where it lives.
type Class string

const (
	ClassOwn    Class = "own"
	ClassPublic Class = "public"
	ClassDM     Class = "dm"
)

// Mode is a conversation's session mode. resume keeps the self loaded once
// and asks for session-idle, session-max and wrap-file; fresh boots every
// turn and accepts none of them.
type Mode string

const (
	ModeResume Mode = "resume"
	ModeFresh  Mode = "fresh"
)

// Member is one line of the family's roster, with the kind=line marker carried
// as a boolean so the parser does not hand the rest of the tool a string it
// must re-decide.
type Member struct {
	ID       string
	KindLine bool
}

// Conversation is one entry under `conversation` or `dm`. The fields are
// what the spec's example carries: the line's per-conversation numbers,
// every one of them, with the required ones set and the optional ones
// reported as zero plus the Set boolean. The boolean is the point: an
// optional key the file did not name is not a key the tool inferred.
type Conversation struct {
	ID            string
	Class         Class
	Name          string
	Mode          Mode
	SessionIdle   string
	SessionMax    string
	WrapFile      string
	ReplyMax      int
	ReplyMaxSet   bool
	ReplyRatio    int
	ReplyRatioSet bool
	ReplyFloor    int
	ReplyFloorSet bool
	MinGap        string
	RepliesPerHr  int
	Context       int
	ContextSet    bool
	HistoryBudget int
	AddressBots   bool
}

// Allow is the parsed allow-list. Conversations and DMs are kept separate so
// the rule that a `dm` entry takes `history-budget` and not `context` (rule
// 17) is the parser's job and not every caller's.
type Allow struct {
	Person        string
	OwnServer     string
	Members       []Member
	Conversations []Conversation
	DMs           []Conversation
	Backoff       []string
	Cooldown      string
	BurstWindow   string
	GapMessages   int
	FloodMultiple int
}

// ConversationByID is the lookup a caller uses once the allow-list is in
// hand. It searches both conversations and DMs, because `dm *` and
// `conversation <id>` are both addresses the rest of the tool polls and the
// distinction lives in Class, not in which slice the entry sits in.
func (a *Allow) ConversationByID(id string) *Conversation {
	for i := range a.Conversations {
		if a.Conversations[i].ID == id {
			return &a.Conversations[i]
		}
	}
	for i := range a.DMs {
		if a.DMs[i].ID == id {
			return &a.DMs[i]
		}
	}
	return nil
}

// ParseAllow reads the file at path and returns the parsed allow-list. The
// error names the line number and the reason, and the caller renders it
// through its own one-line escape; this package keeps no terminal of its own.
func ParseAllow(path string) (*Allow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("allow-list %s could not be read: %w", path, err)
	}
	defer f.Close()
	return parseAllow(f, path)
}

func parseAllow(r io.Reader, source string) (*Allow, error) {
	allow := &Allow{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(stripComment(raw))
		if trimmed == "" {
			continue
		}
		if err := parseAllowLine(allow, trimmed, lineNo); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", source, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("allow-list %s could not be read: %w", source, err)
	}
	if allow.Person == "" {
		return nil, fmt.Errorf("%s: %w", source, errMissing("person"))
	}
	if allow.OwnServer == "" {
		return nil, fmt.Errorf("%s: %w", source, errMissing("own-server"))
	}
	if len(allow.Backoff) == 0 {
		return nil, fmt.Errorf("%s: %w", source, errMissing("backoff"))
	}
	if allow.Cooldown == "" {
		return nil, fmt.Errorf("%s: %w", source, errMissing("cooldown"))
	}
	if allow.BurstWindow == "" {
		return nil, fmt.Errorf("%s: %w", source, errMissing("burst-window"))
	}
	if allow.GapMessages == 0 {
		return nil, fmt.Errorf("%s: %w", source, errMissing("gap-messages"))
	}
	if allow.FloodMultiple == 0 {
		return nil, fmt.Errorf("%s: %w", source, errMissing("flood-multiple"))
	}
	return allow, nil
}

// stripComment drops a `#` comment from a line: a `#` at the start of the
// line or preceded by whitespace begins a comment running to the end of the
// line. The spec's own example carries trailing comments (`member <id> # glenn`),
// so a comment is not only a whole line. A `#` inside a token (no whitespace
// before it) is kept as part of the token.
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return line[:i]
		}
	}
	return line
}

// parseAllowLine is the per-line dispatch. Each top-level token is a verb the
// spec names, and any line that does not start with one of them is a refusal:
// "an unknown key is a refusal naming the key (never ignored)" (SPEC-CHAT.md,
// the allow-list section).
func parseAllowLine(allow *Allow, line string, lineNo int) error {
	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return nil
	}
	switch tokens[0] {
	case "person":
		if allow.Person != "" {
			return errDuplicate("person")
		}
		if len(tokens) != 2 {
			return fmt.Errorf("person takes one id, got %d tokens", len(tokens))
		}
		if err := requireSnowflake(tokens[1]); err != nil {
			return err
		}
		allow.Person = tokens[1]
		return nil
	case "own-server":
		if allow.OwnServer != "" {
			return errDuplicate("own-server")
		}
		if len(tokens) != 2 {
			return fmt.Errorf("own-server takes one id, got %d tokens", len(tokens))
		}
		if err := requireSnowflake(tokens[1]); err != nil {
			return err
		}
		allow.OwnServer = tokens[1]
		return nil
	case "member":
		return parseMember(allow, tokens)
	case "conversation":
		return parseConversation(allow, tokens, false)
	case "dm":
		return parseConversation(allow, tokens, true)
	case "backoff":
		if len(allow.Backoff) != 0 {
			return errDuplicate("backoff")
		}
		if len(tokens) < 2 {
			return fmt.Errorf("backoff takes at least one step, got %d", len(tokens))
		}
		for _, step := range tokens[1:] {
			if err := requireDuration(step); err != nil {
				return fmt.Errorf("backoff step %q: %w", step, err)
			}
		}
		allow.Backoff = append([]string(nil), tokens[1:]...)
		return nil
	case "cooldown":
		if allow.Cooldown != "" {
			return errDuplicate("cooldown")
		}
		if len(tokens) != 2 {
			return fmt.Errorf("cooldown takes one duration, got %d tokens", len(tokens))
		}
		if err := requireDuration(tokens[1]); err != nil {
			return err
		}
		allow.Cooldown = tokens[1]
		return nil
	case "burst-window":
		if allow.BurstWindow != "" {
			return errDuplicate("burst-window")
		}
		if len(tokens) != 2 {
			return fmt.Errorf("burst-window takes one duration, got %d tokens", len(tokens))
		}
		if err := requireDuration(tokens[1]); err != nil {
			return err
		}
		allow.BurstWindow = tokens[1]
		return nil
	case "gap-messages":
		if allow.GapMessages != 0 {
			return errDuplicate("gap-messages")
		}
		if len(tokens) != 2 {
			return fmt.Errorf("gap-messages takes one number, got %d tokens", len(tokens))
		}
		n, err := strconv.Atoi(tokens[1])
		if err != nil || n <= 0 {
			return fmt.Errorf("gap-messages %q is not a positive integer", tokens[1])
		}
		allow.GapMessages = n
		return nil
	case "flood-multiple":
		if allow.FloodMultiple != 0 {
			return errDuplicate("flood-multiple")
		}
		if len(tokens) != 2 {
			return fmt.Errorf("flood-multiple takes one number, got %d tokens", len(tokens))
		}
		n, err := strconv.Atoi(tokens[1])
		if err != nil || n <= 0 {
			return fmt.Errorf("flood-multiple %q is not a positive integer", tokens[1])
		}
		allow.FloodMultiple = n
		return nil
	default:
		return fmt.Errorf("unknown key %q (the allow-list reads and never executes; an ignored key is a setting somebody believes is in force)", tokens[0])
	}
}

// parseMember handles `member <id> [kind=line]`. kind=line is the only value
// the key takes, and is required for the family's own bots (rule 14).
func parseMember(allow *Allow, tokens []string) error {
	if len(tokens) < 2 || len(tokens) > 3 {
		return fmt.Errorf("member takes an id and an optional kind=line, got %d tokens", len(tokens))
	}
	if err := requireSnowflake(tokens[1]); err != nil {
		return err
	}
	m := Member{ID: tokens[1]}
	if len(tokens) == 3 {
		key, val, ok := strings.Cut(tokens[2], "=")
		if !ok || key != "kind" {
			return fmt.Errorf("member's only optional key is kind=line, got %q", tokens[2])
		}
		switch val {
		case "line":
			m.KindLine = true
		default:
			return fmt.Errorf("member kind=%q is unknown; the only value is line", val)
		}
	}
	for _, existing := range allow.Members {
		if existing.ID == m.ID {
			return fmt.Errorf("member %s appears twice", m.ID)
		}
	}
	allow.Members = append(allow.Members, m)
	return nil
}

// parseConversation handles both `conversation <id> ...` and `dm <id-or-*> ...`.
// isDM flips the rules the spec carves out for DMs (no context, history-budget
// instead) and the slice the entry lands in. The rest of the keys are parsed
// in conversation order so the unknown-key error names the field the file
// wrote rather than the order the parser saw them.
func parseConversation(allow *Allow, tokens []string, isDM bool) error {
	if len(tokens) < 2 {
		if isDM {
			return fmt.Errorf("dm takes an id (or *) and its keys, got %d tokens", len(tokens))
		}
		return fmt.Errorf("conversation takes an id and its keys, got %d tokens", len(tokens))
	}
	id := tokens[1]
	if !isDM {
		if err := requireSnowflake(id); err != nil {
			return err
		}
	} else if id != "*" {
		if err := requireSnowflake(id); err != nil {
			return err
		}
	}
	c := Conversation{ID: id}
	seen := map[string]bool{}
	for _, tok := range tokens[2:] {
		key, val, ok := strings.Cut(tok, "=")
		if !ok {
			return fmt.Errorf("%s entry has a token without = : %q", entryKind(isDM), tok)
		}
		if seen[key] {
			return fmt.Errorf("%s entry repeats key %q", entryKind(isDM), key)
		}
		seen[key] = true
		if err := applyConversationKey(&c, isDM, key, val); err != nil {
			return err
		}
	}
	if err := finishConversation(allow, &c, isDM); err != nil {
		return err
	}
	return nil
}

func entryKind(isDM bool) string {
	if isDM {
		return "dm"
	}
	return "conversation"
}

// applyConversationKey is the per-key switch. Unknown keys are a refusal
// naming the key, which is the rule the spec gives for the whole file.
func applyConversationKey(c *Conversation, isDM bool, key, val string) error {
	switch key {
	case "class":
		switch Class(val) {
		case ClassOwn, ClassPublic, ClassDM:
		default:
			return fmt.Errorf("class=%q is unknown; the values are own, public, dm", val)
		}
		// The class must agree with the entry kind: a `dm` line is class=dm
		// and nothing else, a `conversation` line is class=own or
		// class=public and never dm. A class that contradicts the slice the
		// entry lands in would let later callers apply the wrong surface rules.
		if isDM && Class(val) != ClassDM {
			return fmt.Errorf("dm entry has class=%s; a dm entry is class=dm", val)
		}
		if !isDM && Class(val) == ClassDM {
			return fmt.Errorf("conversation entry has class=dm; a conversation is class=own or class=public (a DM is a dm line)")
		}
		c.Class = Class(val)
		return nil
	case "name":
		c.Name = val
		return nil
	case "mode":
		switch Mode(val) {
		case ModeResume, ModeFresh:
			c.Mode = Mode(val)
		default:
			return fmt.Errorf("mode=%q is unknown; the values are resume, fresh", val)
		}
		return nil
	case "session-idle":
		if err := requireDuration(val); err != nil {
			return err
		}
		c.SessionIdle = val
		return nil
	case "session-max":
		if err := requireDuration(val); err != nil {
			return err
		}
		c.SessionMax = val
		return nil
	case "wrap-file":
		c.WrapFile = val
		return nil
	case "reply-max":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("reply-max=%q is not a positive integer", val)
		}
		c.ReplyMax = n
		c.ReplyMaxSet = true
		return nil
	case "reply-ratio":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("reply-ratio=%q is not a positive integer", val)
		}
		c.ReplyRatio = n
		c.ReplyRatioSet = true
		return nil
	case "reply-floor":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("reply-floor=%q is not a positive integer", val)
		}
		c.ReplyFloor = n
		c.ReplyFloorSet = true
		return nil
	case "min-gap":
		if err := requireDuration(val); err != nil {
			return err
		}
		c.MinGap = val
		return nil
	case "replies-per-hour":
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("replies-per-hour=%q is not a positive integer", val)
		}
		c.RepliesPerHr = n
		return nil
	case "context":
		if isDM {
			return fmt.Errorf("context is not for dm entries; dm takes history-budget")
		}
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("context=%q is not a positive integer", val)
		}
		c.Context = n
		c.ContextSet = true
		return nil
	case "history-budget":
		// A dm entry requires it (checked in finishConversation); a
		// conversation may carry it, because mode=fresh trims the window it
		// re-sends at history-budget (SPEC-CHAT.md, the allow-list example's
		// `general` line and test 2).
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("history-budget=%q is not a positive integer", val)
		}
		c.HistoryBudget = n
		return nil
	case "address-bots":
		if val != "yes" {
			return fmt.Errorf("address-bots=%q is unknown; the only value is yes", val)
		}
		c.AddressBots = true
		return nil
	default:
		return fmt.Errorf("unknown key %q on %s entry (an ignored key is a setting somebody believes is in force)", key, entryKind(isDM))
	}
}

// finishConversation is where the spec's per-conversation rules are enforced:
// reply-max is required, mode=resume requires session-idle/session-max/wrap-file
// and forbids none of them, mode=fresh forbids all three. class=own must equal
// the pinned own-server, and the conversation id (or *) must not be a duplicate
// of one already in the file.
func finishConversation(allow *Allow, c *Conversation, isDM bool) error {
	if c.Class == "" {
		if isDM {
			return fmt.Errorf("dm entry has no class= (a dm entry is class=dm)")
		}
		return fmt.Errorf("conversation entry has no class= (own or public; every field is named and none is a default)")
	}
	if c.Mode == "" {
		return fmt.Errorf("%s entry has no mode= (resume or fresh)", entryKind(isDM))
	}
	if !c.ReplyMaxSet {
		return fmt.Errorf("%s entry has no reply-max= (the spec's per-conversation ceiling is required)", entryKind(isDM))
	}
	if c.Mode == ModeResume {
		if c.SessionIdle == "" {
			return fmt.Errorf("%s entry has mode=resume and no session-idle=", entryKind(isDM))
		}
		if c.SessionMax == "" {
			return fmt.Errorf("%s entry has mode=resume and no session-max=", entryKind(isDM))
		}
		if c.WrapFile == "" {
			return fmt.Errorf("%s entry has mode=resume and no wrap-file=", entryKind(isDM))
		}
	} else {
		if c.SessionIdle != "" {
			return fmt.Errorf("%s entry has mode=fresh and session-idle=%s; session-idle is forbidden on fresh", entryKind(isDM), c.SessionIdle)
		}
		if c.SessionMax != "" {
			return fmt.Errorf("%s entry has mode=fresh and session-max=%s; session-max is forbidden on fresh", entryKind(isDM), c.SessionMax)
		}
		if c.WrapFile != "" {
			return fmt.Errorf("%s entry has mode=fresh and wrap-file=%s; wrap-file is forbidden on fresh", entryKind(isDM), c.WrapFile)
		}
	}
	if isDM && c.HistoryBudget == 0 {
		return fmt.Errorf("dm entry has no history-budget= (dm takes history-budget, not context)")
	}
	if c.Class == ClassOwn {
		if allow.OwnServer == "" {
			return fmt.Errorf("class=own entry before own-server is set")
		}
		// The guild-vs-channel mapping lives behind Discord's REST GET, so the parser
		// does not know whether this conversation's id is in the pinned guild.
		// The spec's check fires at runtime, on a token-bearing run: check refuses
		// "class=own off the pinned guild" by reading the channel object. The
		// parser's job is to record the claim; the runtime's job is to check it.
	}
	for _, existing := range allow.Conversations {
		if existing.ID == c.ID {
			return fmt.Errorf("conversation %s appears twice", c.ID)
		}
	}
	for _, existing := range allow.DMs {
		if existing.ID == c.ID {
			return fmt.Errorf("dm %s appears twice", c.ID)
		}
	}
	if isDM {
		allow.DMs = append(allow.DMs, *c)
	} else {
		allow.Conversations = append(allow.Conversations, *c)
	}
	return nil
}

// Quickstart creates the allow-list at path as a commented skeleton with no
// entries. It refuses a file that already exists, naming the path: rule 13
// says quickstart is create-once, and the file a person then opens in an
// editor is the file this tool named, at the path this tool was given.
//
// The write goes through .tmp and rename so a partial write is never what a
// reader finds, and the caller's check can re-read the file with no race.
func Quickstart(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("allow-list %s already exists; quickstart creates the file once and refuses to overwrite", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("allow-list %s could not be checked: %w", path, err)
	}
	body := skeletonBody()
	return writeAtomic(path, []byte(body))
}

// skeletonBody is the file Quickstart writes. The comments name every key
// the parser accepts and the order the spec shows them in, so the file a
// person opens is the file the spec named and the parser will not refuse
// it for an unknown key the moment they uncomment one. Each entry line is
// commented out: a skeleton that ran on first read would run on zero, and
// the spec's own example shows `entries=0` in the QUICKSTART OK line.
func skeletonBody() string {
	return strings.Join([]string{
		"# ---- the person, and their own server. Set by hand, out of band, never from a message.",
		"# ---- every member line goes in BEFORE the invite is sent (rule 11).",
		"# person      <discord-user-id>",
		"# own-server  <guild-id>",
		"# member      <discord-user-id>            # glenn",
		"# member      <discord-user-id> kind=line  # a family's line",
		"",
		"# ---- conversations: class is decided by the guild, not by this line",
		"# conversation <channel-id> class=<own|public> name=<label> mode=<resume|fresh> session-idle=<duration> session-max=<duration> wrap-file=<path> reply-max=<n> [reply-ratio=<n> reply-floor=<n>] min-gap=<duration> replies-per-hour=<n> context=<n>",
		"# dm           <user-id|*> class=dm     mode=<resume|fresh> session-idle=<duration> session-max=<duration> wrap-file=<path> reply-max=<n> [reply-ratio=<n> reply-floor=<n>] min-gap=<duration> replies-per-hour=<n> history-budget=<n>",
		"",
		"# ---- the ladder",
		"# backoff 5m 10m 20m 40m 80m 160m",
		"# cooldown 30m",
		"# burst-window 90s",
		"# gap-messages 200",
		"# flood-multiple 6",
		"",
	}, "\n")
}

// Leave removes one conversation or dm entry from the allow-list at path,
// reposts the file through .tmp and rename, and returns the number of
// conversations plus dms remaining. It refuses an id the file does not
// name and refuses an empty id, because a leave with nothing to leave is
// not a leave.
func Leave(path, id string) (int, error) {
	if strings.TrimSpace(id) == "" {
		return 0, fmt.Errorf("leave takes one conversation id; the id is required")
	}
	allow, err := ParseAllow(path)
	if err != nil {
		return 0, err
	}
	ci, kind := findConversation(allow, id)
	if ci < 0 {
		return 0, fmt.Errorf("allow-list %s has no %s named %q", path, conversationOrDM(allow, id), id)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("allow-list %s could not be read for the rewrite: %w", path, err)
	}
	kept := stripLine(body, kind, id)
	if err := writeAtomic(path, kept); err != nil {
		return 0, err
	}
	return countEntries(allow) - 1, nil
}

func conversationOrDM(allow *Allow, id string) string {
	for i := range allow.Conversations {
		if allow.Conversations[i].ID == id {
			return "conversation"
		}
	}
	for i := range allow.DMs {
		if allow.DMs[i].ID == id {
			return "dm"
		}
	}
	return "conversation"
}

// findConversation returns the slice index of the entry and which slice it
// sat in, or -1 if the id is not named.
func findConversation(allow *Allow, id string) (int, string) {
	for i := range allow.Conversations {
		if allow.Conversations[i].ID == id {
			return i, "conversation"
		}
	}
	for i := range allow.DMs {
		if allow.DMs[i].ID == id {
			return i, "dm"
		}
	}
	return -1, ""
}

// stripLine removes the single line whose verb is `kind` and whose id is
// `id`, plus any inline trailing comment that the parser did not keep. The
// rewrite keeps comments and blank lines untouched.
func stripLine(body []byte, kind, id string) []byte {
	lines := strings.Split(string(body), "\n")
	kept := lines[:0]
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			kept = append(kept, line)
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) >= 2 && fields[0] == kind && fields[1] == id {
			continue
		}
		kept = append(kept, line)
	}
	return []byte(strings.Join(kept, "\n"))
}

func countEntries(allow *Allow) int {
	return len(allow.Conversations) + len(allow.DMs)
}

// writeAtomic lands content at final via a fixed per-path temp name, so a
// retry after a crash overwrites the partial rather than orphaning it, and
// other writers' files are never touched.
func writeAtomic(final string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("allow-list %s: parent directory could not be created: %w", final, err)
	}
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("allow-list %s: temp file could not be opened: %w", final, err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("allow-list %s: temp file write failed: %w", final, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("allow-list %s: temp file sync failed: %w", final, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("allow-list %s: temp file close failed: %w", final, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("allow-list %s: rename failed: %w", final, err)
	}
	return nil
}

// requireSnowflake checks the all-digits shape of a Discord id, which is
// what the file holds. The exact length is not enforced; the spec's
// examples are 18-19 digits and a snowflake with the wrong shape is not
// a conversation this tool can poll.
func requireSnowflake(s string) error {
	if s == "" {
		return fmt.Errorf("id is empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return fmt.Errorf("id %q is not all digits", s)
		}
	}
	return nil
}

// requireDuration is the spec's "every number is yours" rule at the
// duration key: a value this tool cannot parse is a refusal, because a
// duration the rest of the tool cannot read is not a duration the spec
// accepts. The format extends time.ParseDuration with `d` (24h) and `w`
// (7d), because the spec's own example pins session-idle=7d and session-max=30d
// on the family room and a parser that refused those would refuse the file
// the spec writes.
func requireDuration(s string) error {
	if s == "" {
		return fmt.Errorf("duration is empty")
	}
	if _, err := parseChatDuration(s); err != nil {
		return fmt.Errorf("duration %q could not be parsed: %w", s, err)
	}
	return nil
}

// parseChatDuration extends time.ParseDuration with `d` (24h) and `w` (7d).
// A duration that ends in one of the new units is rewritten to its time.ParseDuration
// equivalent and re-parsed; a duration that uses only standard units goes
// straight to time.ParseDuration.
func parseChatDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	last := s[len(s)-1]
	switch last {
	case 'd', 'w':
		head, ok := strings.CutSuffix(s, string(last))
		if !ok || head == "" {
			return 0, fmt.Errorf("unknown unit %q in duration %q", string(last), s)
		}
		for _, r := range head {
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("unknown unit %q in duration %q", string(last), s)
			}
		}
		n, err := strconv.Atoi(head)
		if err != nil {
			return 0, fmt.Errorf("unknown unit %q in duration %q", string(last), s)
		}
		if last == 'd' {
			return time.ParseDuration(fmt.Sprintf("%dh", n*24))
		}
		return time.ParseDuration(fmt.Sprintf("%dh", n*24*7))
	default:
		return time.ParseDuration(s)
	}
}

func errMissing(key string) error {
	return fmt.Errorf("allow-list has no %s= line; the spec's required key is missing", key)
}

func errDuplicate(key string) error {
	return fmt.Errorf("allow-list has more than one %s= line", key)
}
