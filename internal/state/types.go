// Package state provides persistent state management for the virtual filesystem.
package state

// FSState represents the filesystem state
type FSState struct {
	// Map of virtual paths to source paths
	VirtualPaths map[string]string `json:"virtual_paths"`

	// Set of virtual directories (stored as map for quick lookup)
	Directories map[string]bool `json:"directories"`

	// Version for future compatibility
	Version int `json:"version"`
}
