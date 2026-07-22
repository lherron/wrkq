package workrpc

import "testing"

func TestRemoteHookTimeoutLeavesResponseHeadroom(t *testing.T) {
	if RemoteHookTimeoutCeiling <= 0 || RemoteHookTimeoutCeiling >= HTTPResponseTimeout {
		t.Fatalf("remote hook ceiling %s must be positive and below response timeout %s", RemoteHookTimeoutCeiling, HTTPResponseTimeout)
	}
}
