package testutil

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLocalHTTPClientRejectsExternalTargets(t *testing.T) {
	_, err := LocalHTTPClient(time.Second).Get("https://api.example.test/metadata")
	if err == nil || !strings.Contains(err.Error(), "test network isolation") {
		t.Fatalf("external target was not rejected: %v", err)
	}
}

func TestLoopbackNetworkGuardRejectsExternalDials(t *testing.T) {
	previous := http.DefaultTransport
	InstallLoopbackNetworkGuard()
	t.Cleanup(func() { http.DefaultTransport = previous })

	_, err := (&http.Client{Timeout: time.Second}).Get("http://api.example.test/metadata")
	if err == nil || !strings.Contains(err.Error(), "test network isolation") {
		t.Fatalf("default transport allowed external target: %v", err)
	}
}
