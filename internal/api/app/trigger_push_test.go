package app

import "testing"

// Only the process that holds a host's stream tries to push a changed
// trigger set. In API_MODE=http there is none, and trying warned "trigger
// set not pushed: host not connected" on every `triggers add` about a set
// that had already arrived through the store's NOTIFY and the grpc
// replica (I-1162, seen live 2026-11-17).
func TestOnlyTheProcessWithTheStreamPushesASet(t *testing.T) {
	if f := triggerPush(nil, "http"); f != nil {
		t.Error("the http process would send a command it cannot send")
	}
	for _, mode := range []string{"grpc", "all", ""} {
		if f := triggerPush(&App{}, mode); f == nil {
			t.Errorf("API_MODE=%q has the host's stream and does not push", mode)
		}
	}
}
