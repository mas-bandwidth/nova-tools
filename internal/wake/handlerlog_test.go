package wake

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServeLogBoundsOutputWithoutStoppingHandler(t *testing.T) {
	t.Parallel()
	log, err := OpenHandlerLog(filepath.Join(t.TempDir(), "state"), []string{"note-a"})
	if err != nil {
		t.Fatal(err)
	}
	name := log.file.Name()
	payload := bytes.Repeat([]byte("x"), 2*HandlerLogLimit)
	if n, err := log.Write(payload); n != len(payload) || err != nil {
		t.Fatalf("handler output blocked: %d %v", n, err)
	}
	if n, err := log.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("drain stopped: %d %v", n, err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > HandlerLogLimit || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		t.Fatalf("log size or privacy: %+v", st)
	}
	raw, err := os.ReadFile(name)
	if err != nil || !bytes.HasSuffix(raw, []byte(handlerLogTruncated)) || !bytes.Contains(raw, []byte("note-a")) {
		t.Fatalf("missing identity/truncation: size=%d %v", len(raw), err)
	}
}

type brokenServeLog struct{}

func (brokenServeLog) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestServeLogRetainsWriteFailureWhileDraining(t *testing.T) {
	t.Parallel()
	log, err := OpenHandlerLog(filepath.Join(t.TempDir(), "state"), []string{"note-a"})
	if err != nil {
		t.Fatal(err)
	}
	log.writer = brokenServeLog{}
	if n, err := io.Copy(log, strings.NewReader("failure output")); err != nil || n != 14 {
		t.Fatalf("handler stopped: %d %v", n, err)
	}
	if err := log.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost recording failure: %v", err)
	}
}
