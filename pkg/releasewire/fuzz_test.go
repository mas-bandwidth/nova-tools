package releasewire

// FuzzReleasewire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzReleasewire$' -fuzztime=5m ./pkg/releasewire
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
	fuzzBuilder("AdoptCheckEntry", func(b *AdoptCheckEntryBuilder, w []byte, r *TableReport) bool {
		return AdoptCheckEntryLoadBuilder(b, w, r)
	}, func(b *AdoptCheckEntryBuilder, w []byte, r *TableReport) bool {
		return AdoptCheckEntryFromJson(b, w, r)
	}, func(v *AdoptCheckEntry) []byte {
		return fuzzImage(AdoptCheckEntryMeasure(v), func(b []byte) int64 { return AdoptCheckEntrySave(v, b) })
	}, func(v *AdoptCheckEntry) []byte {
		return fuzzImage(AdoptCheckEntryToJsonMeasure(v), func(b []byte) int64 { return AdoptCheckEntryToJson(v, b) })
	}),
	fuzzBuilder("AdoptChecks", func(b *AdoptChecksBuilder, w []byte, r *TableReport) bool { return AdoptChecksLoadBuilder(b, w, r) }, func(b *AdoptChecksBuilder, w []byte, r *TableReport) bool { return AdoptChecksFromJson(b, w, r) }, func(v *AdoptChecks) []byte {
		return fuzzImage(AdoptChecksMeasure(v), func(b []byte) int64 { return AdoptChecksSave(v, b) })
	}, func(v *AdoptChecks) []byte {
		return fuzzImage(AdoptChecksToJsonMeasure(v), func(b []byte) int64 { return AdoptChecksToJson(v, b) })
	}),
	fuzzBuilder("AdoptionChoice", func(b *AdoptionChoiceBuilder, w []byte, r *TableReport) bool {
		return AdoptionChoiceLoadBuilder(b, w, r)
	}, func(b *AdoptionChoiceBuilder, w []byte, r *TableReport) bool { return AdoptionChoiceFromJson(b, w, r) }, func(v *AdoptionChoice) []byte {
		return fuzzImage(AdoptionChoiceMeasure(v), func(b []byte) int64 { return AdoptionChoiceSave(v, b) })
	}, func(v *AdoptionChoice) []byte {
		return fuzzImage(AdoptionChoiceToJsonMeasure(v), func(b []byte) int64 { return AdoptionChoiceToJson(v, b) })
	}),
	fuzzBuilder("AdoptionLedger", func(b *AdoptionLedgerBuilder, w []byte, r *TableReport) bool {
		return AdoptionLedgerLoadBuilder(b, w, r)
	}, func(b *AdoptionLedgerBuilder, w []byte, r *TableReport) bool { return AdoptionLedgerFromJson(b, w, r) }, func(v *AdoptionLedger) []byte {
		return fuzzImage(AdoptionLedgerMeasure(v), func(b []byte) int64 { return AdoptionLedgerSave(v, b) })
	}, func(v *AdoptionLedger) []byte {
		return fuzzImage(AdoptionLedgerToJsonMeasure(v), func(b []byte) int64 { return AdoptionLedgerToJson(v, b) })
	}),
	fuzzBuilder("BuildRecord", func(b *BuildRecordBuilder, w []byte, r *TableReport) bool { return BuildRecordLoadBuilder(b, w, r) }, func(b *BuildRecordBuilder, w []byte, r *TableReport) bool { return BuildRecordFromJson(b, w, r) }, func(v *BuildRecord) []byte {
		return fuzzImage(BuildRecordMeasure(v), func(b []byte) int64 { return BuildRecordSave(v, b) })
	}, func(v *BuildRecord) []byte {
		return fuzzImage(BuildRecordToJsonMeasure(v), func(b []byte) int64 { return BuildRecordToJson(v, b) })
	}),
	fuzzBuilder("CycleBuildArgs", func(b *CycleBuildArgsBuilder, w []byte, r *TableReport) bool {
		return CycleBuildArgsLoadBuilder(b, w, r)
	}, func(b *CycleBuildArgsBuilder, w []byte, r *TableReport) bool { return CycleBuildArgsFromJson(b, w, r) }, func(v *CycleBuildArgs) []byte {
		return fuzzImage(CycleBuildArgsMeasure(v), func(b []byte) int64 { return CycleBuildArgsSave(v, b) })
	}, func(v *CycleBuildArgs) []byte {
		return fuzzImage(CycleBuildArgsToJsonMeasure(v), func(b []byte) int64 { return CycleBuildArgsToJson(v, b) })
	}),
	fuzzBuilder("JourneyEvent", func(b *JourneyEventBuilder, w []byte, r *TableReport) bool { return JourneyEventLoadBuilder(b, w, r) }, func(b *JourneyEventBuilder, w []byte, r *TableReport) bool { return JourneyEventFromJson(b, w, r) }, func(v *JourneyEvent) []byte {
		return fuzzImage(JourneyEventMeasure(v), func(b []byte) int64 { return JourneyEventSave(v, b) })
	}, func(v *JourneyEvent) []byte {
		return fuzzImage(JourneyEventToJsonMeasure(v), func(b []byte) int64 { return JourneyEventToJson(v, b) })
	}),
	fuzzBuilder("JourneyEvidence", func(b *JourneyEvidenceBuilder, w []byte, r *TableReport) bool {
		return JourneyEvidenceLoadBuilder(b, w, r)
	}, func(b *JourneyEvidenceBuilder, w []byte, r *TableReport) bool {
		return JourneyEvidenceFromJson(b, w, r)
	}, func(v *JourneyEvidence) []byte {
		return fuzzImage(JourneyEvidenceMeasure(v), func(b []byte) int64 { return JourneyEvidenceSave(v, b) })
	}, func(v *JourneyEvidence) []byte {
		return fuzzImage(JourneyEvidenceToJsonMeasure(v), func(b []byte) int64 { return JourneyEvidenceToJson(v, b) })
	}),
	fuzzBuilder("JourneyHeader", func(b *JourneyHeaderBuilder, w []byte, r *TableReport) bool { return JourneyHeaderLoadBuilder(b, w, r) }, func(b *JourneyHeaderBuilder, w []byte, r *TableReport) bool { return JourneyHeaderFromJson(b, w, r) }, func(v *JourneyHeader) []byte {
		return fuzzImage(JourneyHeaderMeasure(v), func(b []byte) int64 { return JourneyHeaderSave(v, b) })
	}, func(v *JourneyHeader) []byte {
		return fuzzImage(JourneyHeaderToJsonMeasure(v), func(b []byte) int64 { return JourneyHeaderToJson(v, b) })
	}),
	fuzzBuilder("JourneyInstalled", func(b *JourneyInstalledBuilder, w []byte, r *TableReport) bool {
		return JourneyInstalledLoadBuilder(b, w, r)
	}, func(b *JourneyInstalledBuilder, w []byte, r *TableReport) bool {
		return JourneyInstalledFromJson(b, w, r)
	}, func(v *JourneyInstalled) []byte {
		return fuzzImage(JourneyInstalledMeasure(v), func(b []byte) int64 { return JourneyInstalledSave(v, b) })
	}, func(v *JourneyInstalled) []byte {
		return fuzzImage(JourneyInstalledToJsonMeasure(v), func(b []byte) int64 { return JourneyInstalledToJson(v, b) })
	}),
	fuzzBuilder("MachineEntry", func(b *MachineEntryBuilder, w []byte, r *TableReport) bool { return MachineEntryLoadBuilder(b, w, r) }, func(b *MachineEntryBuilder, w []byte, r *TableReport) bool { return MachineEntryFromJson(b, w, r) }, func(v *MachineEntry) []byte {
		return fuzzImage(MachineEntryMeasure(v), func(b []byte) int64 { return MachineEntrySave(v, b) })
	}, func(v *MachineEntry) []byte {
		return fuzzImage(MachineEntryToJsonMeasure(v), func(b []byte) int64 { return MachineEntryToJson(v, b) })
	}),
	fuzzBuilder("MachineList", func(b *MachineListBuilder, w []byte, r *TableReport) bool { return MachineListLoadBuilder(b, w, r) }, func(b *MachineListBuilder, w []byte, r *TableReport) bool { return MachineListFromJson(b, w, r) }, func(v *MachineList) []byte {
		return fuzzImage(MachineListMeasure(v), func(b []byte) int64 { return MachineListSave(v, b) })
	}, func(v *MachineList) []byte {
		return fuzzImage(MachineListToJsonMeasure(v), func(b []byte) int64 { return MachineListToJson(v, b) })
	}),
	fuzzBuilder("PathsFile", func(b *PathsFileBuilder, w []byte, r *TableReport) bool { return PathsFileLoadBuilder(b, w, r) }, func(b *PathsFileBuilder, w []byte, r *TableReport) bool { return PathsFileFromJson(b, w, r) }, func(v *PathsFile) []byte {
		return fuzzImage(PathsFileMeasure(v), func(b []byte) int64 { return PathsFileSave(v, b) })
	}, func(v *PathsFile) []byte {
		return fuzzImage(PathsFileToJsonMeasure(v), func(b []byte) int64 { return PathsFileToJson(v, b) })
	}),
	fuzzBuilder("ReleaseDigest", func(b *ReleaseDigestBuilder, w []byte, r *TableReport) bool { return ReleaseDigestLoadBuilder(b, w, r) }, func(b *ReleaseDigestBuilder, w []byte, r *TableReport) bool { return ReleaseDigestFromJson(b, w, r) }, func(v *ReleaseDigest) []byte {
		return fuzzImage(ReleaseDigestMeasure(v), func(b []byte) int64 { return ReleaseDigestSave(v, b) })
	}, func(v *ReleaseDigest) []byte {
		return fuzzImage(ReleaseDigestToJsonMeasure(v), func(b []byte) int64 { return ReleaseDigestToJson(v, b) })
	}),
	fuzzBuilder("SnapshotDeliveryEntry", func(b *SnapshotDeliveryEntryBuilder, w []byte, r *TableReport) bool {
		return SnapshotDeliveryEntryLoadBuilder(b, w, r)
	}, func(b *SnapshotDeliveryEntryBuilder, w []byte, r *TableReport) bool {
		return SnapshotDeliveryEntryFromJson(b, w, r)
	}, func(v *SnapshotDeliveryEntry) []byte {
		return fuzzImage(SnapshotDeliveryEntryMeasure(v), func(b []byte) int64 { return SnapshotDeliveryEntrySave(v, b) })
	}, func(v *SnapshotDeliveryEntry) []byte {
		return fuzzImage(SnapshotDeliveryEntryToJsonMeasure(v), func(b []byte) int64 { return SnapshotDeliveryEntryToJson(v, b) })
	}),
	fuzzBuilder("SnapshotDelivery", func(b *SnapshotDeliveryBuilder, w []byte, r *TableReport) bool {
		return SnapshotDeliveryLoadBuilder(b, w, r)
	}, func(b *SnapshotDeliveryBuilder, w []byte, r *TableReport) bool {
		return SnapshotDeliveryFromJson(b, w, r)
	}, func(v *SnapshotDelivery) []byte {
		return fuzzImage(SnapshotDeliveryMeasure(v), func(b []byte) int64 { return SnapshotDeliverySave(v, b) })
	}, func(v *SnapshotDelivery) []byte {
		return fuzzImage(SnapshotDeliveryToJsonMeasure(v), func(b []byte) int64 { return SnapshotDeliveryToJson(v, b) })
	}),
	fuzzBuilder("SnapshotObservedEntry", func(b *SnapshotObservedEntryBuilder, w []byte, r *TableReport) bool {
		return SnapshotObservedEntryLoadBuilder(b, w, r)
	}, func(b *SnapshotObservedEntryBuilder, w []byte, r *TableReport) bool {
		return SnapshotObservedEntryFromJson(b, w, r)
	}, func(v *SnapshotObservedEntry) []byte {
		return fuzzImage(SnapshotObservedEntryMeasure(v), func(b []byte) int64 { return SnapshotObservedEntrySave(v, b) })
	}, func(v *SnapshotObservedEntry) []byte {
		return fuzzImage(SnapshotObservedEntryToJsonMeasure(v), func(b []byte) int64 { return SnapshotObservedEntryToJson(v, b) })
	}),
	fuzzBuilder("SnapshotObserved", func(b *SnapshotObservedBuilder, w []byte, r *TableReport) bool {
		return SnapshotObservedLoadBuilder(b, w, r)
	}, func(b *SnapshotObservedBuilder, w []byte, r *TableReport) bool {
		return SnapshotObservedFromJson(b, w, r)
	}, func(v *SnapshotObserved) []byte {
		return fuzzImage(SnapshotObservedMeasure(v), func(b []byte) int64 { return SnapshotObservedSave(v, b) })
	}, func(v *SnapshotObserved) []byte {
		return fuzzImage(SnapshotObservedToJsonMeasure(v), func(b []byte) int64 { return SnapshotObservedToJson(v, b) })
	}),
	fuzzBuilder("SnapshotScope", func(b *SnapshotScopeBuilder, w []byte, r *TableReport) bool { return SnapshotScopeLoadBuilder(b, w, r) }, func(b *SnapshotScopeBuilder, w []byte, r *TableReport) bool { return SnapshotScopeFromJson(b, w, r) }, func(v *SnapshotScope) []byte {
		return fuzzImage(SnapshotScopeMeasure(v), func(b []byte) int64 { return SnapshotScopeSave(v, b) })
	}, func(v *SnapshotScope) []byte {
		return fuzzImage(SnapshotScopeToJsonMeasure(v), func(b []byte) int64 { return SnapshotScopeToJson(v, b) })
	}),
	fuzzBuilder("SpendReceiptFriend", func(b *SpendReceiptFriendBuilder, w []byte, r *TableReport) bool {
		return SpendReceiptFriendLoadBuilder(b, w, r)
	}, func(b *SpendReceiptFriendBuilder, w []byte, r *TableReport) bool {
		return SpendReceiptFriendFromJson(b, w, r)
	}, func(v *SpendReceiptFriend) []byte {
		return fuzzImage(SpendReceiptFriendMeasure(v), func(b []byte) int64 { return SpendReceiptFriendSave(v, b) })
	}, func(v *SpendReceiptFriend) []byte {
		return fuzzImage(SpendReceiptFriendToJsonMeasure(v), func(b []byte) int64 { return SpendReceiptFriendToJson(v, b) })
	}),
	fuzzBuilder("SpendReceipts", func(b *SpendReceiptsBuilder, w []byte, r *TableReport) bool { return SpendReceiptsLoadBuilder(b, w, r) }, func(b *SpendReceiptsBuilder, w []byte, r *TableReport) bool { return SpendReceiptsFromJson(b, w, r) }, func(v *SpendReceipts) []byte {
		return fuzzImage(SpendReceiptsMeasure(v), func(b []byte) int64 { return SpendReceiptsSave(v, b) })
	}, func(v *SpendReceipts) []byte {
		return fuzzImage(SpendReceiptsToJsonMeasure(v), func(b []byte) int64 { return SpendReceiptsToJson(v, b) })
	}),
	fuzzBuilder("UpdateManifestEntry", func(b *UpdateManifestEntryBuilder, w []byte, r *TableReport) bool {
		return UpdateManifestEntryLoadBuilder(b, w, r)
	}, func(b *UpdateManifestEntryBuilder, w []byte, r *TableReport) bool {
		return UpdateManifestEntryFromJson(b, w, r)
	}, func(v *UpdateManifestEntry) []byte {
		return fuzzImage(UpdateManifestEntryMeasure(v), func(b []byte) int64 { return UpdateManifestEntrySave(v, b) })
	}, func(v *UpdateManifestEntry) []byte {
		return fuzzImage(UpdateManifestEntryToJsonMeasure(v), func(b []byte) int64 { return UpdateManifestEntryToJson(v, b) })
	}),
	fuzzBuilder("UpdateManifest", func(b *UpdateManifestBuilder, w []byte, r *TableReport) bool {
		return UpdateManifestLoadBuilder(b, w, r)
	}, func(b *UpdateManifestBuilder, w []byte, r *TableReport) bool { return UpdateManifestFromJson(b, w, r) }, func(v *UpdateManifest) []byte {
		return fuzzImage(UpdateManifestMeasure(v), func(b []byte) int64 { return UpdateManifestSave(v, b) })
	}, func(v *UpdateManifest) []byte {
		return fuzzImage(UpdateManifestToJsonMeasure(v), func(b []byte) int64 { return UpdateManifestToJson(v, b) })
	}),
	fuzzBuilder("UpdateSnapshot", func(b *UpdateSnapshotBuilder, w []byte, r *TableReport) bool {
		return UpdateSnapshotLoadBuilder(b, w, r)
	}, func(b *UpdateSnapshotBuilder, w []byte, r *TableReport) bool { return UpdateSnapshotFromJson(b, w, r) }, func(v *UpdateSnapshot) []byte {
		return fuzzImage(UpdateSnapshotMeasure(v), func(b []byte) int64 { return UpdateSnapshotSave(v, b) })
	}, func(v *UpdateSnapshot) []byte {
		return fuzzImage(UpdateSnapshotToJsonMeasure(v), func(b []byte) int64 { return UpdateSnapshotToJson(v, b) })
	}),
	fuzzBuilder("VersionSnapEntry", func(b *VersionSnapEntryBuilder, w []byte, r *TableReport) bool {
		return VersionSnapEntryLoadBuilder(b, w, r)
	}, func(b *VersionSnapEntryBuilder, w []byte, r *TableReport) bool {
		return VersionSnapEntryFromJson(b, w, r)
	}, func(v *VersionSnapEntry) []byte {
		return fuzzImage(VersionSnapEntryMeasure(v), func(b []byte) int64 { return VersionSnapEntrySave(v, b) })
	}, func(v *VersionSnapEntry) []byte {
		return fuzzImage(VersionSnapEntryToJsonMeasure(v), func(b []byte) int64 { return VersionSnapEntryToJson(v, b) })
	}),
	fuzzBuilder("VersionSnapshot", func(b *VersionSnapshotBuilder, w []byte, r *TableReport) bool {
		return VersionSnapshotLoadBuilder(b, w, r)
	}, func(b *VersionSnapshotBuilder, w []byte, r *TableReport) bool {
		return VersionSnapshotFromJson(b, w, r)
	}, func(v *VersionSnapshot) []byte {
		return fuzzImage(VersionSnapshotMeasure(v), func(b []byte) int64 { return VersionSnapshotSave(v, b) })
	}, func(v *VersionSnapshot) []byte {
		return fuzzImage(VersionSnapshotToJsonMeasure(v), func(b []byte) int64 { return VersionSnapshotToJson(v, b) })
	}),
	fuzzBuilder("Word", func(b *WordBuilder, w []byte, r *TableReport) bool { return WordLoadBuilder(b, w, r) }, func(b *WordBuilder, w []byte, r *TableReport) bool { return WordFromJson(b, w, r) }, func(v *Word) []byte { return fuzzImage(WordMeasure(v), func(b []byte) int64 { return WordSave(v, b) }) }, func(v *Word) []byte {
		return fuzzImage(WordToJsonMeasure(v), func(b []byte) int64 { return WordToJson(v, b) })
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
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "release", "fuzz", "*")), fuzzImages(t)
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

func FuzzReleasewire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "releasewire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
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

// TestWriteFuzzReleasewireSeeds writes testdata/fuzz/FuzzReleasewire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzReleasewireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzReleasewire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzReleasewire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}
