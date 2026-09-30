package downloader

import "context"

// CapabilitySet is the optional part of an adapter's surface: what the
// configured downloader can do beyond the stable Adapter methods. A nil field
// means the adapter cannot do it, and callers skip that step.
type CapabilitySet struct {
	ListFiles        func(context.Context, string) ([]TorrentFile, error)
	RenameFileInTask func(context.Context, string, string, string) error
	SetPriority      func(context.Context, string, int, int) error
}

// Capabilities probes an adapter once and returns the optional operations it
// supports. Adapters that expose no extra methods get an empty result.
func Capabilities(adapter Adapter) CapabilitySet {
	var capabilities CapabilitySet
	if lister, ok := adapter.(interface {
		Files(context.Context, string) ([]TorrentFile, error)
	}); ok {
		capabilities.ListFiles = lister.Files
	}
	if renamer, ok := adapter.(interface {
		RenameFile(context.Context, string, string, string) error
	}); ok {
		capabilities.RenameFileInTask = renamer.RenameFile
	}
	if priority, ok := adapter.(interface {
		SetFilePriority(context.Context, string, int, int) error
	}); ok {
		capabilities.SetPriority = priority.SetFilePriority
	}
	return capabilities
}
