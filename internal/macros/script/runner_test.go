package script

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/openpaily/paily-check/internal/interfaces"
)

// captureLog runs fn with the standard logger redirected and returns its output.
func captureLog(fn func()) string {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	fn()
	return buf.String()
}

// EngineNative has no fallback: with no handler registered every script returns
// false, so every node is recorded as "does not unlock" with no error anywhere.
// That must be visible, and visible once - RunAllWithEngine runs per node.
func TestWarnNativeWithoutHandlers(t *testing.T) {
	saved := nativeHandlers
	savedOnce := warnNativeOnce
	defer func() { nativeHandlers = saved; warnNativeOnce = savedOnce }()
	nativeHandlers = map[string]NativeHandler{}

	warnNativeOnce = sync.Once{}
	out := captureLog(func() { warnNativeWithoutHandlers(EngineNative) })
	if !strings.Contains(out, "engine=native but no native handler is registered") {
		t.Fatalf("native mode must warn about the empty registry, got %q", out)
	}

	// Second call and every subsequent one must stay silent: the pipeline calls
	// this once per node.
	warnNativeOnce = sync.Once{}
	out = captureLog(func() {
		warnNativeWithoutHandlers(EngineNative)
		warnNativeWithoutHandlers(EngineNative)
		warnNativeWithoutHandlers(EngineNative)
	})
	if n := strings.Count(out, "engine=native"); n != 1 {
		t.Fatalf("warning repeated %d times, want 1 (log: %q)", n, out)
	}
}

func TestWarnNativeAutoIsSofterNote(t *testing.T) {
	saved := nativeHandlers
	savedOnce := warnNativeOnce
	defer func() { nativeHandlers = saved; warnNativeOnce = savedOnce }()
	nativeHandlers = map[string]NativeHandler{}
	warnNativeOnce = sync.Once{}

	out := captureLog(func() { warnNativeWithoutHandlers(EngineAuto) })
	if !strings.Contains(out, "falls back to goja") {
		t.Fatalf("auto mode must note the goja fallback, got %q", out)
	}
	if strings.Contains(out, "unlocked=false") {
		t.Fatalf("auto mode must not claim scripts fail, got %q", out)
	}
}

// A registered handler means no warning at all, and goja mode never warns.
func TestWarnNativeSilentWhenUsable(t *testing.T) {
	saved := nativeHandlers
	savedOnce := warnNativeOnce
	defer func() { nativeHandlers = saved; warnNativeOnce = savedOnce }()

	nativeHandlers = map[string]NativeHandler{"x": func(context.Context, interfaces.Vendor, int) (bool, error) { return true, nil }}
	warnNativeOnce = sync.Once{}
	if out := captureLog(func() { warnNativeWithoutHandlers(EngineNative) }); out != "" {
		t.Fatalf("a registered handler must not warn, got %q", out)
	}

	nativeHandlers = map[string]NativeHandler{}
	warnNativeOnce = sync.Once{}
	if out := captureLog(func() { warnNativeWithoutHandlers(EngineGoja) }); out != "" {
		t.Fatalf("goja mode must not warn, got %q", out)
	}
}
