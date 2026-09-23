package harvest

import (
	"fmt"
	"os"
	"path/filepath"
)

// Capture writes attempt-local evidence under an explicit directory. It does
// not check a RUN action token or fence epoch: private capture is permitted
// during PAUSE and is not an accepted RESULT.
func Capture(dir, name string, body []byte) error {
	if dir == "" || name == "" {
		return fmt.Errorf("harvest: capture dir and name are required; refusing to guess")
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("harvest: capture name must be a single path element")
	}
	return os.WriteFile(filepath.Join(dir, name), body, 0o644)
}
