package oneparser

func parseByte(b []byte) bool {
	if len(b) >= 5 && string(b[:5]) == "HEAD:" {
		return true
	}
	return false
}
