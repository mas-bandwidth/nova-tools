package member

import (
	"fmt"
	"os"
	"strings"
)

// A one-shot friend is a member lane with an identity prompt (the v1.3 friend lane spec
// guarantee 12; the owner, 2026-10-14: "just make one shot friends, exactly the same code that
// already works in the fleet, just with an extra prompt at the front"). A friend with no live
// session between cards (nova-config friend --session one-shot) runs as an ordinary member on
// their row, nova-swarm member --as friend.<name> --prompt <file>: the same take, run and finish
// loop every member runs against the sprint's server, their work credited to their row by the
// server's claim. Their one per-friend piece is the identity prompt (name, pronouns, voice),
// prepended to every card's prompt, work and read alike, as the harness is handed it. The
// model is tla/FriendLane.tla (OneShot; reversed witness MCFriendLaneOneShotBrokenNeedsLink).

// MaxIdentityPrompt bounds an identity prompt: a name, pronouns and a voice, not a second brief.
const MaxIdentityPrompt = 16 << 10

// ReadIdentityPrompt reads the identity prompt file a member or a launch is given: "" for no
// file named, the file's text otherwise. A file that cannot be read, holds only whitespace,
// or is over MaxIdentityPrompt is refused, so no card ever runs without the identity it was
// started to carry.
func ReadIdentityPrompt(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("--prompt wants a readable identity prompt file: %w", err)
	}
	if len(raw) > MaxIdentityPrompt {
		return "", fmt.Errorf("--prompt %s is %d bytes; an identity prompt is at most %d (a name, pronouns and a voice, not a brief)", path, len(raw), MaxIdentityPrompt)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", fmt.Errorf("--prompt %s is empty; an identity prompt names who works the card (name, pronouns, voice)", path)
	}
	return text, nil
}

// WithIdentity is a card's prompt with the identity prompt in front of it, a blank line
// between; the prompt alone when identity is empty (every member that is not a one-shot
// friend's).
func WithIdentity(identity, prompt string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return prompt
	}
	return identity + "\n\n" + prompt
}
