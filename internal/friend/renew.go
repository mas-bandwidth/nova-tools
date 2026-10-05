package friend

import (
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// RenewRoutes is each harness's command for a fresh session in the friend's
// directory with the wake brief as its first turn, as renew says it
// (docs/SPEC-FRIEND.md, Renew); the adapter that runs it is a LaneHarness.
// A harness not here has no new-session route, and renew refuses it with
// nothing changed.
var RenewRoutes = map[string]string{
	"opencode": "opencode run --dir <dir> <the wake brief>",
}

// RenewSeed is the first turn of a renewed session, the wake brief: who the
// friend is, by its own files under its directory (named, never copied), the
// session it replaces and why, who renewed it, and the pong line to run when
// a PING <nonce> arrives (docs/SPEC-FRIEND.md, Renew).
func RenewSeed(friend, coordinator, agents, memory, old, reason, pong string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, in a fresh session: %s renewed your session because the old one could not go on.\n", friend, coordinator)
	switch {
	case agents != "" && memory != "":
		fmt.Fprintf(&b, "Read %s and every file under %s/ first: they are who you are.\n", agents, memory)
	case agents != "":
		fmt.Fprintf(&b, "Read %s first: it is who you are.\n", agents)
	case memory != "":
		fmt.Fprintf(&b, "Read every file under %s/ first: it is who you are.\n", memory)
	}
	fmt.Fprintf(&b, "The old session is %s; it is kept as it was, never deleted or edited.\n", old)
	fmt.Fprintf(&b, "Why it was renewed: %s\n", oneLine(reason, 400))
	fmt.Fprintf(&b, "Your bus messages arrive here as turns from now on. When a message beginning PING <nonce> arrives, run this first, the nonce in place: %s\n", pong)
	b.WriteString("Answer this turn with the one word: ready.\n")
	return b.String()
}

// AgentArgs is the ProgramArguments of an agent's plist, as Plist writes them.
func AgentArgs(plist string) ([]string, error) {
	_, body, ok := strings.Cut(plist, "<key>ProgramArguments</key>")
	if !ok {
		return nil, errors.New("the plist has no ProgramArguments")
	}
	d := xml.NewDecoder(strings.NewReader(body))
	var args []string
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("the plist's ProgramArguments: %v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "string" && depth == 2 {
				var s string
				if err := d.DecodeElement(&s, &t); err != nil {
					return nil, fmt.Errorf("the plist's ProgramArguments: %v", err)
				}
				args = append(args, s)
				depth--
			}
		case xml.EndElement:
			if depth--; depth == 0 {
				return args, nil
			}
		}
	}
}

// ArgValue is the value after flag in args, empty when it is not there.
func ArgValue(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// RepinPlist is plist with its daemon's --session set to session: the value
// replaced where there is one, else the flag added at the end of the daemon's
// command line; the rest of the plist is kept byte for byte.
func RepinPlist(plist, session string) (string, error) {
	args, err := AgentArgs(plist)
	if err != nil {
		return "", err
	}
	if i := slices.Index(args, "--session"); i >= 0 && i+1 < len(args) {
		args[i+1] = session
	} else {
		args = append(args, "--session", session)
	}
	head, rest, _ := strings.Cut(plist, "<key>ProgramArguments</key>")
	open := strings.Index(rest, "<array>")
	end := strings.Index(rest, "</array>")
	if open < 0 || end < open {
		return "", errors.New("the plist's ProgramArguments is no array")
	}
	var b strings.Builder
	b.WriteString("<array>\n")
	for _, a := range args {
		b.WriteString("    <string>" + esc(a) + "</string>\n")
	}
	b.WriteString("  ")
	return head + "<key>ProgramArguments</key>" + rest[:open] + b.String() + rest[end:], nil
}
