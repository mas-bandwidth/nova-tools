package typedrec

import "strings"

// PathsRefusal is the PATHS gate's typed refusal (02_card_move.lua SP.gate:
// the reply of ns_card_push, the why of a REFUSED task move, push or
// unpark), read here and nowhere else (one typed parser for a PATHS line):
//
//	PATHS unbuilt stream=<s>
//	PATHS notopen stream=<s>
//	PATHS overlap paths=<a,b> stream=<s>[|<s2>...]
//
// The streams come last: a name may hold a space, never a '|'.
type PathsRefusal struct {
	Stream  string   // the stream named; an overlap's first
	Also    []string // an overlap's other streams, in the gate's order
	Paths   string   // an overlap's paths, comma-joined as the gate wrote them
	Unbuilt bool     // the stream holds live cards and ws:paths has no field for it
	NotOpen bool     // --join named a stream with no live card
}

// ParsePathsRefusal reads one PATHS gate refusal. ok is false for any other
// why, and for a refusal that names no stream, no paths, or an empty first
// stream.
func ParsePathsRefusal(why string) (PathsRefusal, bool) {
	rest, ok := strings.CutPrefix(why, "PATHS ")
	if !ok {
		return PathsRefusal{}, false
	}
	verdict, fields, _ := strings.Cut(rest, " ")
	switch verdict {
	case "unbuilt", "notopen":
		stream, ok := strings.CutPrefix(fields, "stream=")
		if !ok || stream == "" {
			return PathsRefusal{}, false
		}
		return PathsRefusal{Stream: stream, Unbuilt: verdict == "unbuilt", NotOpen: verdict == "notopen"}, true
	case "overlap":
		csv, ok := strings.CutPrefix(fields, "paths=")
		if !ok {
			return PathsRefusal{}, false
		}
		csv, streams, ok := strings.Cut(csv, " stream=")
		if !ok || csv == "" || streams == "" {
			return PathsRefusal{}, false
		}
		names := strings.Split(streams, "|")
		if names[0] == "" {
			return PathsRefusal{}, false
		}
		return PathsRefusal{Stream: names[0], Also: names[1:], Paths: csv}, true
	}
	return PathsRefusal{}, false
}
