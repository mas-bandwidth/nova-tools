package friendfilewire

// FuzzFriendfilewire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzFriendfilewire$' -fuzztime=5m ./pkg/friendfilewire
// An input is [root selector byte][bytes].

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fuzzForm int

const (
	fuzzWire fuzzForm = iota
	fuzzBase64
	fuzzJSON
	fuzzEmpty // run on a reset value, no input
)

// fuzzRoot is one generated root; run reads in as the form and returns the
// accepted value's wire and JSON images.
type fuzzRoot struct {
	name string
	run  func(f fuzzForm, in []byte, rep *TableReport) (wire, text []byte, ok bool)
}

func (r fuzzRoot) load(f fuzzForm, in []byte) (wire, text []byte, rep TableReport, err error) {
	if f == fuzzBase64 {
		if in, err = base64.StdEncoding.DecodeString(string(in)); err != nil {
			return nil, nil, rep, TableRefuseWireDamaged
		}
	}
	wire, text, ok := r.run(f, in, &rep)
	if err = rep.Err(); err == nil && !ok {
		err = TableRefuseWireDamaged
	}
	return wire, text, rep, err
}

func fuzzB64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func fuzzImage(n int64, save func([]byte) int64) []byte {
	if n < 0 {
		return nil
	}
	b := make([]byte, n)
	if save(b) != n {
		return nil
	}
	return b
}

// fuzzBuilder is a variable table read through its Builder (Lock packs it).
func fuzzBuilder[B any, PB interface {
	*B
	Init(...TableAllocator) bool
	Shutdown()
	Lock() bool
	AsConst() *R
}, R any](name string, loadWire, fromJSON func(PB, []byte, *TableReport) bool, wire, text func(*R) []byte) fuzzRoot {
	return fuzzRoot{name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
		var b B
		pb := PB(&b)
		if !pb.Init() {
			return nil, nil, false
		}
		defer pb.Shutdown()
		ok := f == fuzzEmpty || (f == fuzzJSON && fromJSON(pb, in, rep)) || (f != fuzzJSON && loadWire(pb, in, rep))
		if ok && pb.Lock() {
			return wire(pb.AsConst()), text(pb.AsConst()), true
		}
		return nil, nil, false
	}}
}

// END builder
// fuzzValue is a fixed table, read into a plain value.
func fuzzValue[R any](name string, loadWire, fromJSON func(*R, []byte, *TableReport) bool, wire, text func(*R) []byte) fuzzRoot {
	return fuzzRoot{name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
		var v R
		ok := f == fuzzEmpty || (f == fuzzJSON && fromJSON(&v, in, rep)) || (f != fuzzJSON && loadWire(&v, in, rep))
		if ok {
			return wire(&v), text(&v), true
		}
		return nil, nil, false
	}}
}

// fuzzRoots is every generated root, written by roots.sh.
var fuzzRoots = []fuzzRoot{
	fuzzBuilder("DeliveryCard", func(b *DeliveryCardBuilder, w []byte, r *TableReport) bool { return DeliveryCardLoadBuilder(b, w, r) }, func(b *DeliveryCardBuilder, w []byte, r *TableReport) bool { return DeliveryCardFromJson(b, w, r) }, func(v *DeliveryCard) []byte {
		return fuzzImage(DeliveryCardMeasure(v), func(b []byte) int64 { return DeliveryCardSave(v, b) })
	}, func(v *DeliveryCard) []byte {
		return fuzzImage(DeliveryCardToJsonMeasure(v), func(b []byte) int64 { return DeliveryCardToJson(v, b) })
	}),
	fuzzBuilder("Delivery", func(b *DeliveryBuilder, w []byte, r *TableReport) bool { return DeliveryLoadBuilder(b, w, r) }, func(b *DeliveryBuilder, w []byte, r *TableReport) bool { return DeliveryFromJson(b, w, r) }, func(v *Delivery) []byte {
		return fuzzImage(DeliveryMeasure(v), func(b []byte) int64 { return DeliverySave(v, b) })
	}, func(v *Delivery) []byte {
		return fuzzImage(DeliveryToJsonMeasure(v), func(b []byte) int64 { return DeliveryToJson(v, b) })
	}),
	fuzzBuilder("FriendQueue", func(b *FriendQueueBuilder, w []byte, r *TableReport) bool { return FriendQueueLoadBuilder(b, w, r) }, func(b *FriendQueueBuilder, w []byte, r *TableReport) bool { return FriendQueueFromJson(b, w, r) }, func(v *FriendQueue) []byte {
		return fuzzImage(FriendQueueMeasure(v), func(b []byte) int64 { return FriendQueueSave(v, b) })
	}, func(v *FriendQueue) []byte {
		return fuzzImage(FriendQueueToJsonMeasure(v), func(b []byte) int64 { return FriendQueueToJson(v, b) })
	}),
	fuzzBuilder("FriendRefusal", func(b *FriendRefusalBuilder, w []byte, r *TableReport) bool { return FriendRefusalLoadBuilder(b, w, r) }, func(b *FriendRefusalBuilder, w []byte, r *TableReport) bool { return FriendRefusalFromJson(b, w, r) }, func(v *FriendRefusal) []byte {
		return fuzzImage(FriendRefusalMeasure(v), func(b []byte) int64 { return FriendRefusalSave(v, b) })
	}, func(v *FriendRefusal) []byte {
		return fuzzImage(FriendRefusalToJsonMeasure(v), func(b []byte) int64 { return FriendRefusalToJson(v, b) })
	}),
	fuzzBuilder("FriendStatus", func(b *FriendStatusBuilder, w []byte, r *TableReport) bool { return FriendStatusLoadBuilder(b, w, r) }, func(b *FriendStatusBuilder, w []byte, r *TableReport) bool { return FriendStatusFromJson(b, w, r) }, func(v *FriendStatus) []byte {
		return fuzzImage(FriendStatusMeasure(v), func(b []byte) int64 { return FriendStatusSave(v, b) })
	}, func(v *FriendStatus) []byte {
		return fuzzImage(FriendStatusToJsonMeasure(v), func(b []byte) int64 { return FriendStatusToJson(v, b) })
	}),
	fuzzBuilder("FriendTask", func(b *FriendTaskBuilder, w []byte, r *TableReport) bool { return FriendTaskLoadBuilder(b, w, r) }, func(b *FriendTaskBuilder, w []byte, r *TableReport) bool { return FriendTaskFromJson(b, w, r) }, func(v *FriendTask) []byte {
		return fuzzImage(FriendTaskMeasure(v), func(b []byte) int64 { return FriendTaskSave(v, b) })
	}, func(v *FriendTask) []byte {
		return fuzzImage(FriendTaskToJsonMeasure(v), func(b []byte) int64 { return FriendTaskToJson(v, b) })
	}),
	fuzzBuilder("FriendWatch", func(b *FriendWatchBuilder, w []byte, r *TableReport) bool { return FriendWatchLoadBuilder(b, w, r) }, func(b *FriendWatchBuilder, w []byte, r *TableReport) bool { return FriendWatchFromJson(b, w, r) }, func(v *FriendWatch) []byte {
		return fuzzImage(FriendWatchMeasure(v), func(b []byte) int64 { return FriendWatchSave(v, b) })
	}, func(v *FriendWatch) []byte {
		return fuzzImage(FriendWatchToJsonMeasure(v), func(b []byte) int64 { return FriendWatchToJson(v, b) })
	}),
	fuzzBuilder("LaneEnd", func(b *LaneEndBuilder, w []byte, r *TableReport) bool { return LaneEndLoadBuilder(b, w, r) }, func(b *LaneEndBuilder, w []byte, r *TableReport) bool { return LaneEndFromJson(b, w, r) }, func(v *LaneEnd) []byte {
		return fuzzImage(LaneEndMeasure(v), func(b []byte) int64 { return LaneEndSave(v, b) })
	}, func(v *LaneEnd) []byte {
		return fuzzImage(LaneEndToJsonMeasure(v), func(b []byte) int64 { return LaneEndToJson(v, b) })
	}),
	fuzzBuilder("NoticeEnvelope", func(b *NoticeEnvelopeBuilder, w []byte, r *TableReport) bool {
		return NoticeEnvelopeLoadBuilder(b, w, r)
	}, func(b *NoticeEnvelopeBuilder, w []byte, r *TableReport) bool { return NoticeEnvelopeFromJson(b, w, r) }, func(v *NoticeEnvelope) []byte {
		return fuzzImage(NoticeEnvelopeMeasure(v), func(b []byte) int64 { return NoticeEnvelopeSave(v, b) })
	}, func(v *NoticeEnvelope) []byte {
		return fuzzImage(NoticeEnvelopeToJsonMeasure(v), func(b []byte) int64 { return NoticeEnvelopeToJson(v, b) })
	}),
	fuzzBuilder("NoticeMark", func(b *NoticeMarkBuilder, w []byte, r *TableReport) bool { return NoticeMarkLoadBuilder(b, w, r) }, func(b *NoticeMarkBuilder, w []byte, r *TableReport) bool { return NoticeMarkFromJson(b, w, r) }, func(v *NoticeMark) []byte {
		return fuzzImage(NoticeMarkMeasure(v), func(b []byte) int64 { return NoticeMarkSave(v, b) })
	}, func(v *NoticeMark) []byte {
		return fuzzImage(NoticeMarkToJsonMeasure(v), func(b []byte) int64 { return NoticeMarkToJson(v, b) })
	}),
	fuzzBuilder("NoticeMessage", func(b *NoticeMessageBuilder, w []byte, r *TableReport) bool { return NoticeMessageLoadBuilder(b, w, r) }, func(b *NoticeMessageBuilder, w []byte, r *TableReport) bool { return NoticeMessageFromJson(b, w, r) }, func(v *NoticeMessage) []byte {
		return fuzzImage(NoticeMessageMeasure(v), func(b []byte) int64 { return NoticeMessageSave(v, b) })
	}, func(v *NoticeMessage) []byte {
		return fuzzImage(NoticeMessageToJsonMeasure(v), func(b []byte) int64 { return NoticeMessageToJson(v, b) })
	}),
	fuzzBuilder("NoticeName", func(b *NoticeNameBuilder, w []byte, r *TableReport) bool { return NoticeNameLoadBuilder(b, w, r) }, func(b *NoticeNameBuilder, w []byte, r *TableReport) bool { return NoticeNameFromJson(b, w, r) }, func(v *NoticeName) []byte {
		return fuzzImage(NoticeNameMeasure(v), func(b []byte) int64 { return NoticeNameSave(v, b) })
	}, func(v *NoticeName) []byte {
		return fuzzImage(NoticeNameToJsonMeasure(v), func(b []byte) int64 { return NoticeNameToJson(v, b) })
	}),
	fuzzBuilder("NoticeReceiver", func(b *NoticeReceiverBuilder, w []byte, r *TableReport) bool {
		return NoticeReceiverLoadBuilder(b, w, r)
	}, func(b *NoticeReceiverBuilder, w []byte, r *TableReport) bool { return NoticeReceiverFromJson(b, w, r) }, func(v *NoticeReceiver) []byte {
		return fuzzImage(NoticeReceiverMeasure(v), func(b []byte) int64 { return NoticeReceiverSave(v, b) })
	}, func(v *NoticeReceiver) []byte {
		return fuzzImage(NoticeReceiverToJsonMeasure(v), func(b []byte) int64 { return NoticeReceiverToJson(v, b) })
	}),
}

var fuzzSeeds = map[string]bool{}

func fuzzImages(t testing.TB, globs ...string) (out [][]byte) {
	for _, glob := range globs {
		files, _ := filepath.Glob(glob)
		for _, file := range files {
			b, err := os.ReadFile(file)
			require.NoError(t, err)
			if ext := filepath.Ext(file); ext == ".bin" || ext == ".b64" || ext == ".json" {
				out = append(out, b)
			}
		}
	}
	return
}

// fuzzSeedList is every seed as (selector, bytes): per root the reset value in
// three forms, and every image against every root as wire and base64. The
// schema corpus is this build's own; the others only read or are refused.
func fuzzSeedList(t testing.TB) (sels []uint8, ins [][]byte) {
	add := func(i int, in []byte) { sels, ins = append(sels, uint8(i)), append(ins, in) }
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "friendfile", "fuzz", "*")), fuzzImages(t)
	for i, root := range fuzzRoots {
		var rep TableReport
		if wire, text, _ := root.run(fuzzEmpty, nil, &rep); wire != nil && text != nil {
			fuzzSeeds[string(wire)] = true
			add(i, wire)
			add(i, fuzzB64(wire))
			add(i, text)
		}
		for n, img := range append(append([][]byte{}, corpus...), others...) {
			fuzzSeeds[string(img)] = fuzzSeeds[string(img)] || n < len(corpus)
			add(i, img)
			add(i, fuzzB64(img))
		}
	}
	return
}

func FuzzFriendfilewire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "friendfilewire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
				continue
			}
			require.NotNil(t, wire, "%s as %d %q: an accepted value does not re-encode", root.name, form, in)
			require.NotNil(t, text, "%s as %d %q: an accepted value does not re-encode", root.name, form, in)
			for _, again := range []struct {
				form fuzzForm
				in   []byte
			}{{fuzzWire, wire}, {fuzzBase64, fuzzB64(wire)}, {fuzzJSON, text}} {
				w, tx, _, err := root.load(again.form, again.in)
				require.NoError(t, err, "%s as %d %q: the %d image", root.name, form, in, again.form)
				require.Equal(t, wire, w, "%s as %d %q: the %d image does not read back to itself", root.name, form, in, again.form)
				require.Equal(t, text, tx, "%s as %d %q: the %d image does not read back to itself", root.name, form, in, again.form)
			}
			clean := rep.Unknown == 0 && rep.Widened == 0 && rep.Retained == 0 && rep.Clamped == 0 && rep.KindMismatch == 0 && rep.Duplicate == 0
			if form == fuzzWire && fuzzSeeds[string(in)] && clean {
				require.Equal(t, in, wire, "%s: an accepted seed wire does not reproduce", root.name)
			}
		}
	})
}

// TestWriteFuzzFriendfilewireSeeds writes testdata/fuzz/FuzzFriendfilewire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzFriendfilewireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzFriendfilewire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzFriendfilewire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}
