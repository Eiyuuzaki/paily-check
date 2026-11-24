package engine

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
)

func captureLog(fn func()) string {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	fn()
	return buf.String()
}

// A script error becomes a normal-looking negative result at both call sites, so
// the log line is the only thing that distinguishes "the node does not unlock"
// from "the script is broken". It used to be discarded outright.
func TestThrowExecTaskErrLogsRuntimeFailure(t *testing.T) {
	out := captureLog(func() {
		if !ThrowExecTaskErr("MediaTest", errors.New("boom while running")) {
			t.Fatal("an error must report true")
		}
	})
	if !strings.Contains(out, "MediaTest") || !strings.Contains(out, "boom while running") {
		t.Fatalf("runtime failure must be logged with its scenario, got %q", out)
	}
}

func TestThrowExecTaskErrNamesMissingCallbackSeparately(t *testing.T) {
	out := captureLog(func() {
		if !ThrowExecTaskErr("IPResolve", ErrNoCallback) {
			t.Fatal("a missing callback must still report true")
		}
	})
	if !strings.Contains(out, "does not expose its callback") {
		t.Fatalf("missing callback must be distinguishable from a thrown error, got %q", out)
	}
}

func TestThrowExecTaskErrSilentOnSuccess(t *testing.T) {
	if ThrowExecTaskErr("MediaTest", nil) {
		t.Fatal("nil error must report false")
	}
	if out := captureLog(func() { ThrowExecTaskErr("MediaTest", nil) }); out != "" {
		t.Fatalf("success must not log, got %q", out)
	}
}

func TestIsNotExtractError(t *testing.T) {
	if IsNotExtractError(nil) {
		t.Fatal("nil must not be the sentinel")
	}
	if !IsNotExtractError(ErrNoCallback) {
		t.Fatal("the sentinel itself must match")
	}
	if !IsNotExtractError(errors.Join(errors.New("wrap"), ErrNoCallback)) {
		t.Fatal("a wrapped sentinel must match")
	}
	if IsNotExtractError(errors.New("some other failure")) {
		t.Fatal("an unrelated error must not match")
	}
}

// The sentinel has to survive the wrap that ExecTaskCallback performs, otherwise
// the caller cannot tell a script without a handler from a script that failed.
func TestExecTaskCallbackReturnsCallbackSentinel(t *testing.T) {
	vm := VMNew()
	_, err := ExecTaskCallback(vm, "definitely_not_defined")
	if err == nil {
		t.Fatal("a missing function must be an error")
	}
	if !errors.Is(err, ErrNoCallback) {
		t.Fatalf("err = %v, want ErrNoCallback", err)
	}
}

func TestExecTaskCallbackRejectsNilVM(t *testing.T) {
	if _, err := ExecTaskCallback(nil, "anything"); err == nil {
		t.Fatal("nil vm must be an error")
	}
}

// A script that throws must surface as an error, not a panic.
func TestExecTaskCallbackRecoversPanic(t *testing.T) {
	vm := VMNew()
	if _, err := vm.RunString("function exploding() { throw new Error('from js') }"); err != nil {
		t.Fatalf("failed to install fixture: %v", err)
	}
	_, err := ExecTaskCallback(vm, "exploding")
	if err == nil {
		t.Fatal("a throwing script must return an error")
	}
}
