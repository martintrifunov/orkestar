package daemon

import (
	"fmt"
	"log"
	"runtime/debug"
)

// recoverPanic runs fn and turns any panic into an error instead of letting
// it unwind further, logging the panic value and stack trace for diagnosis.
// Use it where the caller can act on the failure — fail one request, mark
// one session crashed — instead of a bug in a single unit of work taking the
// rest of the daemon down with it.
func recoverPanic(label string, fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("%s: recovered panic: %v\n%s", label, r, debug.Stack())
			err = fmt.Errorf("%s panicked: %v", label, r)
		}
	}()
	fn()
	return nil
}

// guard runs fn as the body of a long-lived goroutine, recovering any panic
// instead of letting it crash the daemon. There is no caller left to hand a
// result to here, so a recovered panic is only logged; the goroutine ends the
// same way it would on any other unrecoverable condition.
func guard(label string, fn func()) {
	_ = recoverPanic(label, fn)
}
