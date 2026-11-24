package engine

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/require"

	"github.com/openpaily/paily-check/internal/engine/factory"
	"github.com/openpaily/paily-check/internal/interfaces"
)

// VMNew creates a bare JS runtime with console and print/debug globals.
func VMNew() *goja.Runtime {
	rt := goja.New()
	new(require.Registry).Enable(rt)
	console.Enable(rt)

	rt.Set("print", factory.PrintFactory(rt, "Script Print |"))
	rt.Set("debug", factory.PrintFactory(rt, "Script Debug |"))

	rt.SetMaxCallStackSize(1024)
	return rt
}

// VMNewWithVendor creates a JS runtime wired to a vendor for fetch/netcat.
func VMNewWithVendor(p interfaces.Vendor, network interfaces.RequestOptionsNetwork) *goja.Runtime {
	vm := VMNew()

	if p != nil {
		pi := p.ProxyInfo()
		vm.Set("proxy", vm.ToValue(pi.Map()))
	} else {
		vm.Set("proxy", nil)
	}

	vm.Set("fetch", factory.FetchFactory(vm, p, network))
	vm.Set("netcat", factory.NetCatFactory(vm, p, network))

	return vm
}

// ErrNoCallback reports that the script does not expose the function the
// caller asked for. It is a sentinel so callers match on errors.Is rather than
// on message text, which would change silently if the wording ever moved.
var ErrNoCallback = errors.New("cannot extract function from vm")

// IsNotExtractError reports whether err is the missing-callback sentinel.
func IsNotExtractError(err error) bool {
	return errors.Is(err, ErrNoCallback)
}

// ThrowExecTaskErr logs the error and reports whether one occurred.
//
// This used to discard the error entirely - the comment claimed it logged, but
// nothing did, so a script with a syntax error, a thrown exception or a timeout
// produced unlocked=false or an empty region with no diagnostic anywhere. Both
// callers turn a failure into a normal-looking negative result, which makes the
// missing log the only thing that would tie the two together.
func ThrowExecTaskErr(scenario string, err error) bool {
	if err == nil {
		return false
	}
	if IsNotExtractError(err) {
		// A script that never defines its callback is an authoring mistake, not a
		// runtime failure: worth naming separately so the reader knows which side
		// to look at first.
		log.Printf("[engine] %s: script does not expose its callback: %v", scenario, err)
		return true
	}
	log.Printf("[engine] %s: %v", scenario, err)
	return true
}

// HasFunction returns true when vm exposes a callable with the given name.
func HasFunction(vm *goja.Runtime, caller string) bool {
	_, ok := goja.AssertFunction(vm.Get(caller))
	return ok
}

// ExecTaskCallback calls a JS function by name with the supplied Go args.
func ExecTaskCallback(vm *goja.Runtime, caller string, args ...interface{}) (ret goja.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ExecTaskCallback panic: %v", r)
		}
	}()

	if vm == nil {
		return goja.Undefined(), fmt.Errorf("vm is not initialized")
	}

	fn, ok := goja.AssertFunction(vm.Get(caller))
	if !ok {
		return goja.Undefined(), fmt.Errorf("%w: %s", ErrNoCallback, caller)
	}

	values := make([]goja.Value, 0, len(args))
	for _, arg := range args {
		values = append(values, vm.ToValue(arg))
	}

	return fn(goja.Undefined(), values...)
}

// RunWithTimeout runs fn in the current goroutine and interrupts the vm if
// timeout elapses before fn returns. timeout ≤ 0 means no timeout.
func RunWithTimeout(vm *goja.Runtime, timeout time.Duration, fn func() (goja.Value, error)) (ret goja.Value, err error) {
	vmLock := sync.Mutex{}
	finished := false

	var timer *time.Timer
	if timeout > 0 {
		// clamp to [1s, 60s]
		if timeout < time.Second {
			timeout = time.Second
		}
		if timeout > time.Minute {
			timeout = time.Minute
		}

		timer = time.AfterFunc(timeout, func() {
			vmLock.Lock()
			defer vmLock.Unlock()
			if !finished {
				finished = true
				vm.Interrupt("script executing too long")
			}
		})
	}

	ret, err = fn()

	vmLock.Lock()
	finished = true
	vmLock.Unlock()

	// Stop the timer when the script finishes first; otherwise it keeps a
	// goroutine alive until the deadline fires a no-op interrupt.
	if timer != nil {
		timer.Stop()
	}

	return
}
