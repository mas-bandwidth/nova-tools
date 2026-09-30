package verbout

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseText parses rendered verb lines back into a Value.
func ParseText(text string) (*Value, error) {
	lines := strings.Split(text, "\n")
	var v *Value
	var token string

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		curToken := fields[0]
		word1 := fields[1]

		if token == "" {
			token = curToken
		}

		if v == nil {
			v = &Value{
				Result: Result{
					Verb: strings.ToLower(token),
				},
				Facts: NewFacts(),
				Token: token,
			}
		}

		// Check for NOTE
		if word1 == "NOTE" {
			noteText := strings.TrimPrefix(line, curToken+" NOTE ")
			v.Note(unescape(noteText))
			continue
		}

		// Check for MORE
		if word1 == "MORE" {
			// Format: <TOKEN> MORE kind=<k> shown=<s> total=<t> [remedy]
			var kind, rem string
			var shown, total int
			remIdx := -1
			for i := 2; i < len(fields); i++ {
				k, val, ok := strings.Cut(fields[i], "=")
				if !ok {
					remIdx = i
					break
				}
				switch k {
				case "kind":
					kind = unescape(val)
				case "shown":
					shown, _ = strconv.Atoi(val)
				case "total":
					total, _ = strconv.Atoi(val)
				}
			}
			if remIdx != -1 {
				rem = strings.Join(fields[remIdx:], " ")
			}
			v.AddMore(kind, shown, total, unescape(rem))
			continue
		}

		// Check for Result line: OK, REFUSED, or FAIL with key=value fields
		isResult := false
		switch word1 {
		case "OK", "REFUSED":
			isResult = true
		case "FAIL":
			// If word2 has '=' it's a result line like LINKS FAIL files=2 broken=1;
			// otherwise it's an item finding like LINKS FAIL path/to/file:1: ...
			if len(fields) > 2 && strings.Contains(fields[2], "=") {
				isResult = true
			}
		}

		if isResult {
			switch word1 {
			case "OK":
				v.Result.Status = StatusOK
				v.Result.Exit = 0
			case "FAIL":
				v.Result.Status = StatusFailed
				v.Result.Exit = 1
			case "REFUSED":
				v.Result.Status = StatusRefused
				v.Result.Exit = 2
			}

			// Check for remedy: '; run: <remedy>'
			lineFacts := line
			if idx := strings.Index(line, "; run: "); idx != -1 {
				v.Result.Remedy = unescape(line[idx+len("; run: "):])
				lineFacts = line[:idx]
			}

			factWords := strings.Fields(lineFacts)
			for i := 2; i < len(factWords); i++ {
				if k, val, ok := strings.Cut(factWords[i], "="); ok {
					v.Fact(k, unescape(val))
					if k == "exit" || k == "worst-exit" {
						if code, err := strconv.Atoi(val); err == nil {
							v.Result.Exit = code
						}
					}
				} else {
					v.Fact(factWords[i], "")
				}
			}
			continue
		}

		// Otherwise, it is an Item line
		kind := word1
		prefix := curToken + " " + kind
		itemText := strings.TrimPrefix(line, prefix)
		itemText = strings.TrimPrefix(itemText, " ")
		v.Item(kind, itemText)
	}

	if v == nil {
		return nil, fmt.Errorf("verbout: no valid verb lines found in input")
	}

	return v, nil
}

// ParseLine parses a single result line into a Value.
func ParseLine(line string) (*Value, error) {
	return ParseText(line)
}

func unescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if val, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(val))
				i += 4
				continue
			}
		} else if s[i] == '\\' && i+5 < len(s) && s[i+1] == 'u' {
			if val, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
				b.WriteRune(rune(val))
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
