package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestUIPrinters(t *testing.T) {
	out := captureStdout(t, func() {
		printHeader("Header")
		printSuccess("ok")
		printInfo("info")
		printWarning("warn")
		printError("err")
		printUpdate("upd")
		printPackage("pkg", "1.0.0")
		printDim("dim")
		printDownload("dl")
		printCached()
		printTransitive("dep", "2.0.0")
	})
	for _, part := range []string{"Header", "ok", "info", "warn", "err", "upd", "pkg @1.0.0", "dim", "dl", "cached", "dep@2.0.0"} {
		if !strings.Contains(out, part) {
			t.Fatalf("missing %q in output: %q", part, out)
		}
	}
}
