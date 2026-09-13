//go:build !gui || (!darwin && !windows)

package desktop

// Start is a no-op in server builds. Desktop builds enable the gui tag on
// native macOS or Windows runners, keeping ordinary Go cross-compilation
// independent of platform GUI toolchains.
func Start(_, _, _ string) {}

func Stop() {}
