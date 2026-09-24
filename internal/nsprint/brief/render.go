package brief

import (
	"bytes"
	"context"
	"io"
	"os"
	"text/template"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// Render renders the brief of the task <sprint>/<id> in two sequential round
// trips, never pipelined (round trip 2 carries a digest of bytes that exist
// only after round trip 1's reply):
//
//  1. HMGET title kind; both nil refuses got=MISSING.
//  2. In process: render the bytes, digest the template and the brief.
//  3. FCALL ns_task_brief with the exact title and kind from step 1.
//  4. Only on OK are the bytes written to out (a file) or stdout.
//
// Exit 0 rendered; 1 refused; 2 usage error or Redis unreachable.
func Render(ctx context.Context, st *store.Store, ref, out string, stdout, stderr io.Writer) int {
	sprint, id, err := parseRef(ref)
	if err != nil {
		printf(stderr, "nova-sprint brief render: %v\n", err)
		return 2
	}
	title, kind, err := task.ReadTitleKind(ctx, st, sprint, id)
	if err != nil {
		printf(stderr, "nova-sprint brief render: %v\n", err)
		return 2
	}
	if !title.Set && !kind.Set {
		printf(stderr, "BRIEF REFUSED task=%s field=task got=MISSING\n", id)
		return 1
	}
	tmplBytes, ok := templateFor(kind.Value)
	if !ok {
		printf(stderr, "BRIEF REFUSED task=%s field=kind want=%s got=%s\n", id, Kinds(), orAbsent(kind))
		return 1
	}
	f, err := taskFields(sprint, id, title.Value)
	if err != nil {
		printf(stderr, "BRIEF REFUSED task=%s field=%s got=ABSENT\n", id, err.Error())
		return 1
	}
	tmpl, err := template.New(kind.Value).Option("missingkey=error").Parse(string(tmplBytes))
	if err != nil {
		printf(stderr, "nova-sprint brief render: template %s: %v\n", kind.Value, err)
		return 2
	}
	tmplSHA := sha256hex(tmplBytes)
	var b bytes.Buffer
	b.WriteString(headerTask + sprint + "/" + id + "\n")
	b.WriteString(headerRules + tmplSHA + "\n")
	if err := tmpl.Execute(&b, f); err != nil {
		printf(stderr, "nova-sprint brief render: template %s: %v\n", kind.Value, err)
		return 2
	}
	briefSHA := sha256hex(b.Bytes())

	if afterRead != nil {
		afterRead()
	}
	status, _, err := task.RecordBrief(ctx, st, sprint, id, title.Value, kind.Value, tmplSHA, briefSHA)
	if err != nil {
		printf(stderr, "nova-sprint brief render: %v\n", err)
		return 2
	}
	if status != task.BriefOK {
		printf(stderr, "BRIEF REFUSED task=%s field=task got=%s\n", id, status)
		return 1
	}
	if out == "" {
		if _, err := stdout.Write(b.Bytes()); err != nil {
			printf(stderr, "nova-sprint brief render: %v\n", err)
			return 2
		}
		return 0
	}
	if err := os.WriteFile(out, b.Bytes(), 0o644); err != nil {
		printf(stderr, "nova-sprint brief render: %v\n", err)
		return 2
	}
	printf(stdout, "BRIEF RENDERED task=%s out=%s sha=%s\n", id, out, briefSHA)
	return 0
}

func orAbsent(f task.Field) string {
	if !f.Set || f.Value == "" {
		return absent
	}
	return f.Value
}
