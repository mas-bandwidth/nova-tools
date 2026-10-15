package friend

import "fmt"

// PongLine is a session's pong as it travels on the bus: pong <nonce>
// queue=<n> working=<n> width=<n>. A session sends it to answer a reach step
// or a delivery check (nova-friend pong); liveness is the nova-hook record's
// alone, never a pong.
func PongLine(nonce string, queue, working, width int) string {
	return fmt.Sprintf("pong %s queue=%d working=%d width=%d", nonce, queue, working, width)
}

// ParsePong reads a pong line; ok is false when the text is no pong.
func ParsePong(text string) (nonce string, queue, working, width int, ok bool) {
	n, err := fmt.Sscanf(text, "pong %s queue=%d working=%d width=%d", &nonce, &queue, &working, &width)
	if err != nil || n < 1 {
		var only string
		if k, _ := fmt.Sscanf(text, "pong %s", &only); k == 1 {
			return only, 0, 0, 0, true // the numbers are optional
		}
		return "", 0, 0, 0, false
	}
	return nonce, queue, working, width, true
}
