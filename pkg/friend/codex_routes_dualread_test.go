package friend

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/require"
)

// legacyRoutesJSON is the sprint routes view as nova-sprint writes it today:
// the legacy form codexRouteModels read before the dual read.
const legacyRoutesJSON = `{"tier_routes":{"pro":["pro-codex"]},"routes":[{"route":{"name":"pro-codex","tier":"pro","provider":"subscription-codex","model":"gpt-5.6-sol","harness":"codex","enabled":true,"prices":{"billing":"subscription"}}}]}`

// routesSchemaJSON is the same view in the schema's RoutesView JSON text form:
// a tier's applied names sit under an object's "names" array.
const routesSchemaJSON = `{"tier_routes":{"pro":{"names":[{"s":"pro-codex"}]}},"routes":[{"route":{"name":"pro-codex","tier":"pro","provider":"subscription-codex","model":"gpt-5.6-sol","harness":"codex","enabled":true,"prices":{"billing":"subscription"}}}]}`

// buildRoutesReader is the routes-view reader over pkg/schemaread and the
// generated RoutesView table (pkg/answerwire). It reads the binary wire, the
// padded standard base64 of it, the JSON text form and the legacy form, and
// refuses damage by name.
func buildRoutesReader() (func([]byte) (codexRoutesView, schemaread.Form, error), *schemaread.Reader[answerwire.RoutesView]) {
	var cur codexRoutesView
	reportOf := func(rep answerwire.TableReport, ok bool) schemaread.Report {
		out := schemaread.Report{
			Unknown:      int(rep.Unknown),
			KindMismatch: int(rep.KindMismatch),
			Clamped:      int(rep.Clamped),
			Malformed:    rep.Malformed || !ok,
		}
		if rep.Verdict == answerwire.TableOpenRefused || rep.Verdict == answerwire.TableOpenDamaged {
			if err := rep.Err(); err != nil {
				out.Reason = err.Error()
			}
		}
		return out
	}
	loadWire := func(v *answerwire.RoutesView, wire []byte) schemaread.Report {
		var b answerwire.RoutesViewBuilder
		if !b.Init() {
			return schemaread.Report{Malformed: true}
		}
		defer b.Shutdown()
		var rep answerwire.TableReport
		ok := answerwire.RoutesViewLoadBuilder(&b, wire, &rep)
		out := reportOf(rep, ok)
		if ok && b.Lock() {
			cur = convertRoutesView(b.AsConst())
		}
		return out
	}
	loadJSON := func(v *answerwire.RoutesView, text []byte) schemaread.Report {
		var b answerwire.RoutesViewBuilder
		if !b.Init() {
			return schemaread.Report{Malformed: true}
		}
		defer b.Shutdown()
		var rep answerwire.TableReport
		ok := answerwire.RoutesViewFromJson(&b, text, &rep)
		out := reportOf(rep, ok)
		if ok && b.Lock() {
			cur = convertRoutesView(b.AsConst())
		}
		return out
	}
	loadLegacy := func(v *answerwire.RoutesView, in []byte) schemaread.Report {
		if err := json.Unmarshal(in, &cur); err != nil {
			return schemaread.Report{Malformed: true}
		}
		return schemaread.Report{}
	}
	saveWire := func(v *answerwire.RoutesView) []byte {
		var b answerwire.RoutesViewBuilder
		if !b.Init() {
			return nil
		}
		defer b.Shutdown()
		var rep answerwire.TableReport
		if !answerwire.RoutesViewFromJson(&b, []byte(routesSchemaJSON), &rep) || !b.Lock() {
			return nil
		}
		root := b.AsConst()
		n, err := answerwire.RoutesViewMeasureReason(root)
		if err != nil || n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if answerwire.RoutesViewSave(root, buf) != n {
			return nil
		}
		return buf
	}
	saveJSON := func(v *answerwire.RoutesView) []byte {
		var b answerwire.RoutesViewBuilder
		if !b.Init() {
			return nil
		}
		defer b.Shutdown()
		var rep answerwire.TableReport
		if !answerwire.RoutesViewFromJson(&b, []byte(routesSchemaJSON), &rep) || !b.Lock() {
			return nil
		}
		root := b.AsConst()
		n := answerwire.RoutesViewToJsonMeasure(root)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if answerwire.RoutesViewToJson(root, buf) != n {
			return nil
		}
		return buf
	}
	reader := schemaread.NewReader(&schemaread.Codec[answerwire.RoutesView]{
		Name:       "ROUTES",
		Help:       "nova-sprint routes --json",
		Reset:      func(v *answerwire.RoutesView) { answerwire.RoutesViewReset(v) },
		Measure:    func(wire []byte) error { _, err := answerwire.RoutesViewLoadMeasureReason(wire); return err },
		LoadWire:   loadWire,
		LoadJSON:   loadJSON,
		Legacy:     func(in []byte) bool { s := string(in); return strings.Contains(s, "\"tier_routes\"") && !strings.Contains(s, "\"names\"") },
		LoadLegacy: loadLegacy,
		SaveWire:   saveWire,
		SaveJSON:   saveJSON,
	})
	install := func(in []byte) (codexRoutesView, schemaread.Form, error) {
		cur = codexRoutesView{}
		_, res, err := reader.Read(in)
		if err != nil {
			return codexRoutesView{}, res.Form, err
		}
		return cur, res.Form, nil
	}
	return install, reader
}

// convertRoutesView copies a live RoutesView table into the plain codexRoutesView
// the adapter reads, while the builder's arena is alive.
func convertRoutesView(v *answerwire.RoutesView) codexRoutesView {
	var out codexRoutesView
	if v == nil {
		return out
	}
	if v.TierRoutes.Count > 0 {
		out.TierRoutes = map[string][]string{}
		for i := int32(0); i < v.TierRoutes.Count; i++ {
			e := v.TierRoutes.At(i)
			key := string(e.Key[:e.KeyLength])
			names := make([]string, 0, e.Value.NamesCount)
			for j := int32(0); j < e.Value.NamesCount; j++ {
				n := &e.Value.Names[j]
				names = append(names, string(n.S[:n.SLength]))
			}
			out.TierRoutes[key] = names
		}
	}
	if v.Routes.Count > 0 {
		out.Routes = make([]struct {
			Route struct {
				Name, Tier, Provider, Model, Harness string
				Enabled                              bool
				Prices                               struct{ Billing string }
			} `json:"route"`
		}, 0, v.Routes.Count)
		for i := int32(0); i < v.Routes.Count; i++ {
			st := v.Routes.At(i)
			r := st.Route
			out.Routes = append(out.Routes, struct {
				Route struct {
					Name, Tier, Provider, Model, Harness string
					Enabled                              bool
					Prices                               struct{ Billing string }
				} `json:"route"`
			}{Route: struct {
				Name, Tier, Provider, Model, Harness string
				Enabled                              bool
				Prices                               struct{ Billing string }
			}{
				Name:     string(r.Name[:r.NameLength]),
				Tier:     string(r.Tier[:r.TierLength]),
				Provider: string(r.Provider[:r.ProviderLength]),
				Model:    string(r.Model[:r.ModelLength]),
				Harness:  string(r.Harness[:r.HarnessLength]),
				Enabled:   r.Enabled,
				Prices:    struct{ Billing string }{Billing: string(r.Prices.Billing[:r.Prices.BillingLength])},
			}})
		}
	}
	return out
}

// wantRoutes is the codexRoutesView both forms decode to.
func wantRoutes() codexRoutesView {
	return codexRoutesView{
		TierRoutes: map[string][]string{"pro": {"pro-codex"}},
		Routes: []struct {
			Route struct {
				Name, Tier, Provider, Model, Harness string
				Enabled                              bool
				Prices                               struct{ Billing string }
			} `json:"route"`
		}{{Route: struct {
			Name, Tier, Provider, Model, Harness string
			Enabled                              bool
			Prices                               struct{ Billing string }
		}{
			Name: "pro-codex", Tier: "pro", Provider: "subscription-codex", Model: "gpt-5.6-sol", Harness: "codex", Enabled: true,
			Prices: struct{ Billing string }{Billing: "subscription"},
		}}},
	}
}

func TestFriendCodexRoutesDualRead(t *testing.T) {
	t.Parallel()

	install, reader := buildRoutesReader()
	InstallRoutesViewRead(install)

	jsonForm, err := reader.Write(newRoutesValue(), schemaread.FormJSON)
	require.NoError(t, err)
	wire, err := reader.Write(newRoutesValue(), schemaread.FormBinary)
	require.NoError(t, err)
	base64Form := base64.StdEncoding.EncodeToString(wire)

	want := wantRoutes()

	t.Run("every form reads alike", func(t *testing.T) {
		for _, c := range []struct {
			name string
			in   string
			form schemaread.Form
		}{
			{"binary", string(wire), schemaread.FormBinary},
			{"base64", base64Form, schemaread.FormBase64},
			{"json", string(jsonForm), schemaread.FormJSON},
			{"legacy", legacyRoutesJSON, schemaread.FormLegacy},
		} {
			t.Run(c.name, func(t *testing.T) {
				got, form, err := install([]byte(c.in))
				require.NoError(t, err)
				require.Equal(t, c.form, form)
				require.Equal(t, want, got)
			})
		}
		counts := reader.Counts()
		require.Equal(t, uint64(1), counts.Reads(schemaread.FormBinary), "binary reads")
		require.Equal(t, uint64(1), counts.Reads(schemaread.FormBase64), "base64 reads")
		require.Equal(t, uint64(1), counts.Reads(schemaread.FormJSON), "json reads")
		require.Equal(t, uint64(1), counts.Reads(schemaread.FormLegacy), "legacy reads")
	})

	t.Run("refusals by name and never absent", func(t *testing.T) {
		bad := map[string]string{
			"truncated binary": string(wire[:len(wire)/2]),
			"truncated base64": base64Form[:len(base64Form)/2],
			"truncated json":   string(jsonForm[:len(jsonForm)/2]),
			"flipped form byte": string(flip(wire, 0)),
		}
		for name, in := range bad {
			t.Run(name, func(t *testing.T) {
				_, _, err := install([]byte(in))
				require.Error(t, err)
				require.Contains(t, err.Error(), "ROUTES REFUSED")
			})
		}
		t.Run("clamped", func(t *testing.T) {
			clamped := `{"tier_routes":{"pro":{"names":[{"s":"pro-codex"}]}},"routes":[{"route":{"name":"` + strings.Repeat("n", 100) + `","tier":"pro","provider":"subscription-codex","model":"gpt-5.6-sol","harness":"codex","enabled":true,"prices":{"billing":"subscription"}}}]}`
			_, _, err := install([]byte(clamped))
			require.Error(t, err)
			require.Contains(t, err.Error(), "ROUTES REFUSED")
		})
	})
}

func newRoutesValue() *answerwire.RoutesView {
	var b answerwire.RoutesViewBuilder
	if !b.Init() {
		return nil
	}
	var rep answerwire.TableReport
	if !answerwire.RoutesViewFromJson(&b, []byte(routesSchemaJSON), &rep) || !b.Lock() {
		return nil
	}
	return b.AsConst()
}

func flip(b []byte, i int) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	out[i] ^= 0x01
	return out
}
