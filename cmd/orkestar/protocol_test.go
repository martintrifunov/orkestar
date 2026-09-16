package main

import (
	"strconv"
	"testing"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// Compatibility is the protocol generation, not the build version: two builds
// that speak the same protocol interoperate.
func TestProtocolCompatible(t *testing.T) {
	compatible := func(status map[string]string) bool {
		ok, _ := protocolCompatible(status)
		return ok
	}

	if !compatible(map[string]string{"protocol": strconv.Itoa(ipc.Version), "version": "9.9.9"}) {
		t.Fatal("a different build on the same protocol should be compatible")
	}
	if compatible(map[string]string{"protocol": strconv.Itoa(ipc.Version + 1), "version": "9.9.9"}) {
		t.Fatal("a different protocol should be refused")
	}

	// A daemon old enough not to advertise a protocol is judged by build.
	if compatible(map[string]string{"version": "whatever"}) {
		t.Fatal("an older daemon on a different build should be refused")
	}
	if !compatible(map[string]string{"version": version}) {
		t.Fatal("an older daemon on the same build should be compatible")
	}
	if compatible(map[string]string{"version": version + "-other"}) {
		t.Fatal("an older daemon on a different build should be refused")
	}
}
