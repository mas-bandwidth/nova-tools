package store

import (
	"os"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
)

// ExitDown is the one exit code for a store that did not answer or whose
// ACL refused the seat's user: the code land, consume, reconcile and sprint
// open used ("6 no Redis"), now every refusal's that Explain classifies so.
const ExitDown = 6

// Class is which of the store's own failures a cause is.
type Class string

const (
	// ClassNone is a cause that is not the store's.
	ClassNone Class = ""
	// ClassNoFn is the nova_sprint function library not loaded on the store.
	ClassNoFn Class = "nofn"
	// ClassDown is a store that did not answer (a dial, a timeout, a dropped
	// connection); a login it refused is not this class (NoAnswerText).
	ClassDown Class = "down"
	// ClassNoPerm is the seat's ACL user denied a command.
	ClassNoPerm Class = "noperm"
)

// noPermUser reads the ACL user out of Redis's own NOPERM line ("NOPERM User
// audit has no permissions to run the 'fcall' command").
var noPermUser = regexp.MustCompile(`NOPERM User (\S+) `)

// causeAddr is the first host:port a cause names ("dial tcp 10.0.0.5:6379:
// connect: connection refused", "redis at 127.0.0.1:6379: ...").
var causeAddr = regexp.MustCompile(`(?:\d{1,3}(?:\.\d{1,3}){3}|localhost|[A-Za-z][\w.-]*\.[A-Za-z]\w*):\d{2,5}\b`)

// Explain reads a refusal's cause for the store's own errors and gives each
// class one remedy and one exit code, whichever verb (or package) hit it,
// so every line names the same next verb: the nova_sprint function library
// not loaded ("ERR Function not found") is `nova-sprint fn load --redis
// <addr>`, exit 2; a store that did not answer is `--redis` / `nova-sprint
// doctor`, ExitDown; the seat's ACL user denied (NOPERM) names that user and
// `nova-sprint acl check`, ExitDown. The address is the cause's own
// host:port (a dial error carries it), else the one this process last
// opened (LastOpened), since a Redis reply names none; with neither the
// remedy spells the flag. A cause of no store class comes back as given,
// exit 2, ClassNone.
func Explain(cause string) (line string, code int, class Class) {
	addr := LastOpened()
	if m := causeAddr.FindString(cause); m != "" {
		addr = m
	}
	flag := "--redis <addr>"
	at := "the store"
	if addr != "" {
		flag, at = "--redis "+addr, addr
	}
	switch {
	case strings.Contains(cause, "Function not found"):
		return cause + "; the nova_sprint function library is not loaded on " + at +
			"; run: nova-sprint fn load " + flag + " (nova-sprint doctor " + flag + " shows the store)", 2, ClassNoFn
	case strings.Contains(cause, "NOPERM"):
		user := "the seat's ACL user"
		if m := noPermUser.FindStringSubmatch(cause); m != nil {
			user = "ACL user " + m[1]
		} else if u := os.Getenv(redisauth.UserEnv); u != "" {
			user = "ACL user " + u + " (" + redisauth.UserEnv + ")"
		}
		return cause + "; " + at + " denies " + user + " (the seat's row in seats.tsv, else " + redisauth.UserEnv +
			"); run: nova-sprint acl check " + flag + " (the play writes the rows), or nova-sprint doctor " + flag, ExitDown, ClassNoPerm
	case NoAnswerText(cause):
		return cause + "; " + at + " did not answer; check " + flag + " (or NOVA_SPRINT_REDIS), then run: nova-sprint doctor " + flag, ExitDown, ClassDown
	}
	return cause, 2, ClassNone
}

// ExplainErr is Explain for an error: the explained line and true for a
// store class, else the error's own text and false.
func ExplainErr(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	line, _, class := Explain(err.Error())
	return line, class != ClassNone
}
