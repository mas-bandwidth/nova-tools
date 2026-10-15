package friend

import (
	"fmt"
	"time"
)

// PingText is a coordinator PING as the daemon's tests send it. The coordinator's
// ping verbs and loops that sent one are retired (v1.3.5,
// internal/ci/testdata/retired-presence.txt): liveness is the nova-hook server's
// challenge, so only these tests still drive the daemon's PING path.
func PingText(seat string, since time.Time, nonce string) string {
	return fmt.Sprintf("PING %s\nseat=%s since=%s\nAnswer first, before anything else, with one command: nova-friend pong --as <you> --nonce %s --queue <tasks queued> --working <tasks working> --width <your width>\nThen go on with what you were doing.", nonce, seat, since.UTC().Format(time.RFC3339), nonce)
}

// WakePingText is a wake check as the daemon's tests send it: the ping, and WakeMark.
func WakePingText(seat string, since time.Time, nonce string) string {
	return PingText(seat, since, nonce) + "\n" + WakeMark
}
