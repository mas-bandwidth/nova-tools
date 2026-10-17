package swarm

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/swarmwire"
)

// DeclareRouteModel names a launch's model in the job's harness config as a model of its
// provider (`provider.<provider>.models.<model>: {}`), so the harness knows the model
// whatever its catalog holds at start. An entry already there is kept as it is.
//
// Every job runs the harness in a fresh data home, so the model catalog fetches at each
// start; when that fetch is slow or loses the race it falls back to the snapshot built
// into the binary, which predates the newer models, and the launch fails with
// `ProviderModelNotFoundError`. Declaring the model in the harness config prevents that
// failure whatever the catalog fetch yields.
//
// ok is false when the config is not a JSON object or the model is not provider/model;
// the body is then returned unchanged.
func DeclareRouteModel(body []byte, provider, model string) ([]byte, bool) {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" || model == "" {
		return body, false
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return body, false
	}
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		if _, other := cfg["provider"]; other {
			return body, false
		}
		providers = map[string]any{}
		cfg["provider"] = providers
	}
	entry, _ := providers[provider].(map[string]any)
	if entry == nil {
		if _, other := providers[provider]; other {
			return body, false
		}
		entry = map[string]any{}
		providers[provider] = entry
	}
	models, _ := entry["models"].(map[string]any)
	if models == nil {
		if _, other := entry["models"]; other {
			return body, false
		}
		models = map[string]any{}
		entry["models"] = models
	}
	if _, declared := models[model]; !declared {
		models[model] = map[string]any{}
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return body, false
	}
	return out, true
}

// declaredModelDoc is the declared model record as the schema reads it: the
// plain value the generated DeclaredModel table's strict codec reads and
// writes (schema section 6, the schema JSON text form). The legacy form is the
// JSON object the writer carries today, which reads into the same value.
type declaredModelDoc struct {
	Provider, Model string
}

// declaredModelOf is a loaded table image as the doc.
func declaredModelOf(r *swarmwire.DeclaredModel) declaredModelDoc {
	return declaredModelDoc{exitText(&r.Provider), exitText(&r.Model)}
}

func (d declaredModelDoc) json() []byte {
	m := map[string]any{"provider": d.Provider, "model": d.Model}
	b, _ := json.MarshalIndent(m, "", "  ")
	return b
}

func declaredModelBuild(d declaredModelDoc) (*swarmwire.DeclaredModel, func()) {
	var b swarmwire.DeclaredModelBuilder
	if !b.Init() {
		return nil, func() {}
	}
	var r swarmwire.TableReport
	if !swarmwire.DeclaredModelFromJson(&b, d.json(), &r) || !b.Lock() {
		return nil, b.Shutdown
	}
	return b.AsConst(), b.Shutdown
}

// declaredModelReader is the declared model record's strict reader (owner's
// rules S1 and S2): it reads the table wire, the padded standard base64 of
// that wire, the schema JSON text and the legacy JSON the writer carries
// today, refuses a damaged image by name and counts each read under its form.
func declaredModelReader() *schemaread.Reader[declaredModelDoc] {
	var zero declaredModelDoc
	c := &schemaread.Codec[declaredModelDoc]{
		Name: "DECLARED_MODEL", Help: "nova-swarm -h",
		LoadWire: func(v *declaredModelDoc, wire []byte) schemaread.Report {
			*v = zero
			n := swarmwire.DeclaredModelLoadMeasure(wire)
			if n < 0 {
				return exitStrict(swarmwire.TableReport{Malformed: true}, false)
			}
			var r swarmwire.TableReport
			root := swarmwire.DeclaredModelLoad(exitRegion(n), wire, &r)
			if root == nil {
				return exitStrict(r, false)
			}
			*v = declaredModelOf(root)
			return exitStrict(r, true)
		},
		LoadJSON: func(v *declaredModelDoc, text []byte) schemaread.Report {
			*v = zero
			var b swarmwire.DeclaredModelBuilder
			if !b.Init() {
				return exitStrict(swarmwire.TableReport{}, false)
			}
			defer b.Shutdown()
			var r swarmwire.TableReport
			if !swarmwire.DeclaredModelFromJson(&b, text, &r) || !b.Lock() {
				return exitStrict(r, false)
			}
			*v = declaredModelOf(b.AsConst())
			return exitStrict(r, true)
		},
		Legacy: func(in []byte) bool { return len(in) > 0 && in[0] == '{' },
		LoadLegacy: func(v *declaredModelDoc, in []byte) schemaread.Report {
			if err := json.Unmarshal(in, v); err != nil {
				return schemaread.Report{Malformed: true}
			}
			return schemaread.Report{}
		},
		Constraints: func(v *declaredModelDoc, c *schemaread.Checker) {
			c.Text("provider", v.Provider, math.MaxInt)
			c.Text("model", v.Model, math.MaxInt)
		},
		SaveWire: func(v *declaredModelDoc) []byte {
			root, done := declaredModelBuild(*v)
			defer done()
			if root == nil {
				return nil
			}
			buf := make([]byte, swarmwire.DeclaredModelMeasure(root))
			if swarmwire.DeclaredModelSave(root, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
		SaveJSON: func(v *declaredModelDoc) []byte {
			root, done := declaredModelBuild(*v)
			defer done()
			if root == nil {
				return nil
			}
			buf := make([]byte, swarmwire.DeclaredModelToJsonMeasure(root))
			if swarmwire.DeclaredModelToJson(root, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
	}
	return schemaread.NewReader(c)
}

// ReadDeclaredModel reads the declared model record from body in any form and
// hands the refusal to the caller by name; a malformed value is never read as
// absent, empty or zero.
func ReadDeclaredModel(body []byte) (declaredModelDoc, schemaread.Form, error) {
	d, res, err := declaredModelReader().Read(body)
	return d, res.Form, err
}
