package ws

import (
	"reflect"
	"strings"
	"testing"
)

// devParseRefusal is origin/dev 415b6df38's ParseRefusal body.
func devParseRefusal(why string) (*PathsRefusal, bool) {
	if rest, ok := strings.CutPrefix(why, "PATHS unbuilt stream="); ok && rest != "" {
		return &PathsRefusal{Stream: rest, Unbuilt: true}, true
	}
	if rest, ok := strings.CutPrefix(why, "PATHS notopen stream="); ok && rest != "" {
		return &PathsRefusal{Stream: rest, NotOpen: true}, true
	}
	rest, ok := strings.CutPrefix(why, "PATHS overlap paths=")
	if !ok {
		return nil, false
	}
	csv, stream, ok := strings.Cut(rest, " stream=")
	if !ok || csv == "" || stream == "" {
		return nil, false
	}
	streams := strings.Split(stream, "|")
	if streams[0] == "" {
		return nil, false
	}
	return &PathsRefusal{Stream: streams[0], Also: streams[1:], Paths: ParsePaths(csv)}, true
}

// TestRead4429ParseRefusalMatchesDev (rowan-opus cold read of #4429): the
// three SP.gate forms (02_card_move.lua:398/:400/:435) and near misses read
// the same through typedrec.ParsePathsRefusal as through dev's parser.
func TestRead4429ParseRefusalMatchesDev(t *testing.T) {
	t.Parallel()
	for _, why := range []string{
		"PATHS unbuilt stream=work", "PATHS unbuilt stream=swarm: cards", "PATHS notopen stream=a b",
		"PATHS overlap paths=internal/x,internal/x/y.go stream=s1", "PATHS overlap paths=a stream=s1|s2|s3",
		"PATHS overlap paths=a stream=s|", "PATHS overlap paths=a,b stream=x stream=y", "PATHS overlap paths=a stream=|s2",
		"PATHS unbuilt stream=", "PATHS notopen stream=", "PATHS unbuilt  stream=x", "PATHS unbuilt stream=x|y",
		"PATHS overlap paths= stream=a", "PATHS overlap paths=a", "PATHS", "PATHS ", "", "PATHS unread id=x in=y",
		"REFUSED PATHS unbuilt stream=w", "PATHS overlap  paths=a stream=b", "PATHS unbuiltx stream=a",
	} {
		got, gok := ParseRefusal(why)
		want, wok := devParseRefusal(why)
		if gok != wok || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: head %v %+v, dev %v %+v", why, gok, got, wok, want)
		}
	}
}
