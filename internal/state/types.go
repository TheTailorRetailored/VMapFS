// Package state provides persistent state management for the virtual filesystem.
package state

// FSState represents the filesystem state
type FSState struct {
	// Map of source paths to their virtual path and xattrs
	Mappings map[string]FileMapping `json:"mappings"`
	// Set of virtual directories (stored as map for quick lookup)
	Directories map[string]bool `json:"directories"`
	// Version for future compatibility
	Version int `json:"version"`
}

// FileMapping represents a single file's virtual mapping and attributes
type FileMapping struct {
	VirtualPath string            `json:"virtual_path"`
	Xattrs      map[string][]byte `json:"xattrs,omitempty"`
}
