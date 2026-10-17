package release

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdoptMachinesDualRead(t *testing.T) {
	t.Parallel()

	const jsonText = `{"machines":[{"name":"bench-1"},{"name":"bench-2","bin":"/opt/bin","dest":"/opt/dest"}]}`
	want := []Machine{
		{Name: "bench-1"},
		{Name: "bench-2", Bin: "/opt/bin", Dest: "/opt/dest"},
	}

	list := buildMachineList(t, jsonText)
	wire := machineWire(t, list)
	b64 := []byte(base64.StdEncoding.EncodeToString(wire))
	legacy := "bench-1\n# a comment\n\nbench-2\t/opt/bin\t/opt/dest\n"

	forms := map[string][]byte{
		"binary": wire,
		"base64": b64,
		"json":   []byte(jsonText),
		"legacy": []byte(legacy),
	}

	for name, form := range forms {
		name, form := name, form
		t.Run("reads "+name, func(t *testing.T) {
			t.Parallel()
			got, err := Machines(strings.NewReader(string(form)))
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}

	t.Run("reads are counted per record and per form", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(machineListCodec())
		for f := schemaread.FormBinary; f <= schemaread.FormLegacy; f++ {
			_, _, err := r.Read(forms[f.String()])
			require.NoError(t, err)
		}
		counts := r.Counts()
		for f := schemaread.FormBinary; f <= schemaread.FormLegacy; f++ {
			assert.Equal(t, uint64(1), counts.Reads(f), "%s read", f)
		}
	})

	bad := map[string][]byte{
		"binary": wire[:len(wire)-1],
		"base64": append(append([]byte{}, b64...), '!'),
		"json":   []byte(`{"machines":[{"name":`),
		"legacy": []byte("bench-1\t/bin\t/dest\t/extra\n"),
	}
	for name, form := range bad {
		name, form := name, form
		t.Run("refuses malformed "+name, func(t *testing.T) {
			t.Parallel()
			_, err := Machines(strings.NewReader(string(form)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "REFUSED")
			assert.Contains(t, err.Error(), "run:")
		})
	}
}

func buildMachineList(t *testing.T, jsonText string) *releasewire.MachineList {
	t.Helper()
	var b releasewire.MachineListBuilder
	require.True(t, b.Init())
	var rep releasewire.TableReport
	require.True(t, releasewire.MachineListFromJson(&b, []byte(jsonText), &rep))
	require.True(t, b.Lock())
	return b.AsConst()
}

func machineWire(t *testing.T, l *releasewire.MachineList) []byte {
	t.Helper()
	n := releasewire.MachineListMeasure(l)
	buf := make([]byte, n)
	require.Equal(t, n, releasewire.MachineListSave(l, buf))
	return buf
}
