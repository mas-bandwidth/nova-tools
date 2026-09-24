package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"path"
	"strings"
	"text/template"
)

// Render executes a template from fs with custom delims [[ ]] to prevent
// clashes with Go source syntax. If the target is Go source, it passes it
// through format.Source (gofmt).
func Render(fs embed.FS, templatePath string, data any) ([]byte, error) {
	name := path.Base(templatePath)
	t, err := template.New(name).Delims("[[", "]]").ParseFS(fs, templatePath)
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", templatePath, err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", templatePath, err)
	}
	if strings.HasSuffix(templatePath, ".go.tmpl") {
		src, err := format.Source(b.Bytes())
		if err != nil {
			return nil, fmt.Errorf("%s renders invalid Go: %w\n%s", templatePath, err, b.String())
		}
		return src, nil
	}
	return b.Bytes(), nil
}
