//go:build gui && (darwin || windows)

package desktop

import "testing"

func TestPortFromListenAddressIgnoresKernelAssignedPort(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:0":     "",
		"127.0.0.1:63710": "63710",
		":7789":           "7789",
		"localhost":       "",
		"":                "",
	}
	for value, want := range cases {
		if got := portFromListenAddress(value); got != want {
			t.Errorf("portFromListenAddress(%q) = %q, want %q", value, got, want)
		}
	}
}
