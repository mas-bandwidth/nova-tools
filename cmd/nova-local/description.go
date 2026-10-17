package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/swarmwire"
)

// Description is a worker description in nova-swarm's schema (pkg/swarm/worker.go,
// Worker, decoded with DisallowUnknownFields): exactly its twelve keys, board omitted
// when not given, and no temperature, seed or num_ctx, which serve baked into the served
// tag (rule 6); no prompt conditions either, which nova-swarm owns (rule 11).
type Description struct {
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	BaseURL     string   `json:"base_url"`
	EnvVar      string   `json:"env_var"`
	KeyFile     string   `json:"key_file"`
	Usage       string   `json:"usage"`
	Harness     string   `json:"harness"`
	HarnessArgs []string `json:"harness_args"`
	WorkerDir   string   `json:"worker_dir"`
	Deadline    string   `json:"deadline"`
	Board       string   `json:"board,omitempty"`
}

// The usage sources nova-swarm reads (pkg/swarm, UsageOpenCode and UsageNone).
var usages = []string{"opencode", "none"}

// SplitArgs is --harness-args split on commas, with no escape.
func SplitArgs(s string) []string { return strings.Split(s, ",") }

// Problems is every independent reason the description cannot be written, each naming
// the field and the command that satisfies it (rule 14, test 15): {model} placed in the
// harness's argv, an absolute existing worker directory, a key file that exists and is
// not empty (stat'ed, never opened), a usage nova-swarm reads, a Go duration.
func (d Description) Problems(stat func(string) (os.FileInfo, error)) []string {
	var out []string
	joined := strings.Join(d.HarnessArgs, "\x00")
	if !strings.Contains(joined, "{model}") {
		out = append(out, "--harness-args wants {model} placed in the harness's argv (such as run,--model,ollama/{model},--,{prompt}); without it the harness is told no model")
	}
	switch fi, err := stat(d.WorkerDir); {
	case !filepath.IsAbs(d.WorkerDir):
		out = append(out, fmt.Sprintf("--worker-dir wants an absolute directory (got %q): mkdir -p $PWD/home and pass $PWD/home", d.WorkerDir))
	case err != nil || !fi.IsDir():
		out = append(out, fmt.Sprintf("--worker-dir %s is not a directory: mkdir -p %s", d.WorkerDir, d.WorkerDir))
	}
	switch fi, err := stat(d.KeyFile); {
	case err != nil:
		out = append(out, fmt.Sprintf("--key-file %s does not exist; the local engine wants no key, and nova-swarm a non-empty file: printf 'local\\n' > %s && chmod 600 %s", d.KeyFile, d.KeyFile, d.KeyFile))
	case fi.Size() == 0:
		out = append(out, fmt.Sprintf("--key-file %s is empty; nova-swarm refuses an empty key file: printf 'local\\n' > %s && chmod 600 %s", d.KeyFile, d.KeyFile, d.KeyFile))
	}
	if !slices.Contains(usages, d.Usage) {
		out = append(out, fmt.Sprintf("--usage wants opencode (OpenCode's own accounting) or none (got %q)", d.Usage))
	}
	if _, err := time.ParseDuration(d.Deadline); err != nil {
		out = append(out, fmt.Sprintf("--deadline wants a Go duration such as 20m (got %q)", d.Deadline))
	}
	return out
}

// Write writes the description to path, the one file this tool writes (rule 11), as the
// schema JSON text form of WorkerRecord (owner's rule S4, writers second: nova-swarm's
// LoadWorker reads every form; and the ruling on files read directly: a person reads and
// writes this file, so it is the JSON text, never binary or base64). A value the table
// cannot hold is refused by name before a byte is written; there is no legacy fallback.
func (d Description) Write(path string) error {
	raw, err := descriptionWrites.Write(&d, schemaread.FormJSON)
	if err != nil {
		return err
	}
	if raw == nil {
		return &schemaread.Refusal{Name: "WORKER_RECORD", Reason: schemaread.ReasonMalformed, Help: "nova-local worker -h"}
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// workerRecordUsages is usage as WorkerRecord's uint8 (schemas/swarm/swarm.schema,
// WorkerRecord usage): 1 opencode, 2 none; zero is "none declared", which LoadWorker refuses.
var workerRecordUsages = map[string]uint8{"opencode": 1, "none": 2}

// descriptionWrites is WorkerRecord's write side for this tool (schema section 6: the
// constraints the reader refuses after a load, the writer refuses before it writes; owner's
// rule S2, the JSON text form). Write checks the description, then hands it to SaveJSON.
var descriptionWrites = schemaread.NewReader(&schemaread.Codec[Description]{
	Name: "WORKER_RECORD", Help: "nova-local worker -h",
	Constraints: descriptionConstraints,
	SaveJSON:    descriptionSaveJSON,
})

// descriptionConstraints are the bounds WorkerRecord's reader in nova-swarm enforces, stated
// for the writer: every text UTF-8 with no NUL, and usage a word the table has an ordinal for.
func descriptionConstraints(d *Description, c *schemaread.Checker) {
	for _, f := range d.texts() {
		c.Text(f[0], f[1], math.MaxInt)
	}
	for _, s := range d.HarnessArgs {
		c.Text("harness_args", s, math.MaxInt)
	}
	c.Required("usage", d.Usage != "")
	ordinal := -1
	if u, ok := workerRecordUsages[d.Usage]; ok {
		ordinal = int(u)
	}
	c.Enum("usage", ordinal, len(workerRecordUsages)+1)
}

// texts is every text field by its WorkerRecord name.
func (d *Description) texts() [][2]string {
	return [][2]string{{"name", d.Name}, {"provider", d.Provider}, {"model", d.Model}, {"base_url", d.BaseURL},
		{"env_var", d.EnvVar}, {"key_file", d.KeyFile}, {"harness", d.Harness}, {"worker_dir", d.WorkerDir},
		{"deadline", d.Deadline}, {"board", d.Board}}
}

// descriptionSaveJSON is the checked description as WorkerRecord's schema JSON text: usage
// as its ordinal, harness_args as the table's list of Entry, an empty text absent (the
// schema's Absence rule), built through the generated builder and written by the generated
// ToJson, then indented for the person who reads the file; nil when the table does not build.
func descriptionSaveJSON(d *Description) []byte {
	doc := map[string]any{"usage": workerRecordUsages[d.Usage]}
	for _, f := range d.texts() {
		if f[1] != "" {
			doc[f[0]] = f[1]
		}
	}
	if len(d.HarnessArgs) > 0 {
		args := make([]map[string]string, 0, len(d.HarnessArgs))
		for _, s := range d.HarnessArgs {
			args = append(args, map[string]string{"value": s})
		}
		doc["harness_args"] = args
	}
	text, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	var b swarmwire.WorkerRecordBuilder
	if !b.Init() {
		return nil
	}
	defer b.Shutdown()
	var rep swarmwire.TableReport
	if !swarmwire.WorkerRecordFromJson(&b, text, &rep) || rep.Err() != nil || !b.Lock() {
		return nil
	}
	root := b.AsConst()
	out := make([]byte, swarmwire.WorkerRecordToJsonMeasure(root))
	if swarmwire.WorkerRecordToJson(root, out) != int64(len(out)) {
		return nil
	}
	var indented bytes.Buffer
	if json.Indent(&indented, out, "", "  ") != nil {
		return nil
	}
	return indented.Bytes()
}
