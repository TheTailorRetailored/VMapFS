package fs

import (
	"os"
	"path/filepath"
	"strings"

	"vmapfs/internal/logging"
	"vmapfs/internal/state"
)

var (
	pathLogger = logging.GetLogger().WithPrefix("path")
)

// SourcePath represents a path in the actual source filesystem.
// All paths are stored relative to the source root directory.
type SourcePath struct {
	// relative path from source root
	path string
}

// NewSourcePath creates a new SourcePath instance.
// It cleans the path and ensures it's relative to the source root.
func NewSourcePath(path string) *SourcePath {
	// Clean and ensure relative
	cleaned := filepath.Clean(path)
	cleaned = strings.TrimPrefix(cleaned, "/")
	pathLogger.Trace("Creating new source path: %q -> %q", path, cleaned)
	return &SourcePath{path: cleaned}
}

// String returns the string representation of the path
func (sp *SourcePath) String() string {
	return sp.path
}

// FullPath returns the absolute path by joining with the source root
func (sp *SourcePath) FullPath(sourceRoot string) string {
	full := filepath.Join(sourceRoot, sp.path)
	pathLogger.Trace("Getting full path: %q + %q -> %q", sourceRoot, sp.path, full)
	return full
}

// Parent returns a SourcePath representing the parent directory
func (sp *SourcePath) Parent() *SourcePath {
	parent := filepath.Dir(sp.path)
	if parent == "." {
		parent = ""
	}
	pathLogger.Trace("Getting parent path: %q -> %q", sp.path, parent)
	return NewSourcePath(parent)
}

// Base returns the last element of the path
func (sp *SourcePath) Base() string {
	return filepath.Base(sp.path)
}

// VirtualPath represents a path in our virtual filesystem.
// All paths are absolute and never include special directories like _UNSORTED.
type VirtualPath struct {
	// always starts with /, never includes _UNSORTED
	path string
}

// NewVirtualPath creates a new VirtualPath instance.
// It cleans the path and ensures it's absolute.
func NewVirtualPath(path string) *VirtualPath {
	cleaned := filepath.Clean(path)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	pathLogger.Trace("Creating new virtual path: %q -> %q", path, cleaned)
	return &VirtualPath{path: cleaned}
}

// String returns the string representation of the path
func (vp *VirtualPath) String() string {
	return vp.path
}

// Parent returns a VirtualPath representing the parent directory
func (vp *VirtualPath) Parent() *VirtualPath {
	return NewVirtualPath(filepath.Dir(vp.path))
}

// Base returns the last element of the path
func (vp *VirtualPath) Base() string {
	return filepath.Base(vp.path)
}

// IsRoot returns true if this is the root virtual path "/"
func (vp *VirtualPath) IsRoot() bool {
	return vp.path == "/"
}

// PathMapper handles mapping between virtual and source paths.
type PathMapper struct {
	mappings   map[string]state.FileMapping // source path -> FileMapping
	sourceRoot string
	logger     *logging.Logger
}

// NewPathMapper creates a new PathMapper instance with the given source root
// and initial mappings.
func NewPathMapper(sourceRoot string, mappings map[string]state.FileMapping) *PathMapper {
	logger := logging.GetLogger().WithPrefix("pathmap")
	logger.Debug("Creating new path mapper for root: %s", sourceRoot)

	return &PathMapper{
		mappings:   mappings,
		sourceRoot: sourceRoot,
		logger:     logger,
	}
}

// IsPathMapped returns true if the source path has a virtual mapping
func (pm *PathMapper) IsPathMapped(sp *SourcePath) bool {
	mapping, exists := pm.mappings[sp.String()]
	// Consider it mapped only if it exists and has a non-empty virtual path
	mapped := exists && mapping.VirtualPath != ""
	pm.logger.Trace("Checking if path is mapped: %q (exists=%v, virtual_path=%q, mapped=%v)", sp.String(), exists, mapping.VirtualPath, mapped)
	return mapped
}

// GetVirtualPath returns the virtual path for a source path, if one exists
func (pm *PathMapper) GetVirtualPath(sp *SourcePath) (*VirtualPath, bool) {
	mapping, exists := pm.mappings[sp.String()]
	pm.logger.Trace("Looking up virtual path: %q -> %q (exists=%v)", sp.String(), mapping.VirtualPath, exists)
	if !exists {
		return nil, false
	}
	return NewVirtualPath(mapping.VirtualPath), true
}

// GetSourcePath returns the source path for a virtual path, if one exists
func (pm *PathMapper) GetSourcePath(vp *VirtualPath) (*SourcePath, bool) {
	for spath, mapping := range pm.mappings {
		if mapping.VirtualPath == vp.String() {
			pm.logger.Trace("Looking up source path: %q -> %q (exists=true)", vp.String(), spath)
			return NewSourcePath(spath), true
		}
	}
	pm.logger.Trace("Source path not found for: %q", vp.String())
	return nil, false
}

// AddMapping creates a new virtual->source path mapping
func (pm *PathMapper) AddMapping(vp *VirtualPath, sp *SourcePath) {
	fullPath := sp.FullPath(pm.sourceRoot)
	info, err := os.Stat(fullPath)
	if err != nil {
		pm.logger.Warn("Cannot stat source path %q: %v", fullPath, err)
		return
	}
	if info.IsDir() {
		pm.logger.Warn("Rejecting directory mapping: %q", sp.String())
		return
	}

	pm.logger.Debug("Adding mapping: %q -> %q", vp.String(), sp.String())
	mapping, exists := pm.mappings[sp.String()]
	if !exists {
		mapping = state.FileMapping{Xattrs: make(map[string][]byte)}
	}
	mapping.VirtualPath = vp.String()
	pm.mappings[sp.String()] = mapping
}

// RemoveMapping removes a virtual->source path mapping
func (pm *PathMapper) RemoveMapping(vp *VirtualPath) {
	pm.logger.Debug("Removing mapping for: %q", vp.String())
	for spath, mapping := range pm.mappings {
		if mapping.VirtualPath == vp.String() {
			mapping.VirtualPath = ""
			pm.mappings[spath] = mapping
			return
		}
	}
}

// GetXattrs returns the extended attributes for a source path
func (pm *PathMapper) GetXattrs(sp *SourcePath) (map[string][]byte, bool) {
	mapping, exists := pm.mappings[sp.String()]
	if !exists {
		pm.logger.Trace("No xattrs found for source path: %q", sp.String())
		return nil, false
	}
	return mapping.Xattrs, true
}

// SetXattr sets an extended attribute for a source path
func (pm *PathMapper) SetXattr(sp *SourcePath, name string, value []byte) {
	pm.logger.Debug("Setting xattr %q for source path %q", name, sp.String())
	mapping, exists := pm.mappings[sp.String()]
	if !exists {
		mapping = state.FileMapping{Xattrs: make(map[string][]byte)}
	}
	if mapping.Xattrs == nil {
		mapping.Xattrs = make(map[string][]byte)
	}
	mapping.Xattrs[name] = value
	pm.mappings[sp.String()] = mapping
}

// RemoveXattr removes an extended attribute for a source path
func (pm *PathMapper) RemoveXattr(sp *SourcePath, name string) {
	pm.logger.Debug("Removing xattr %q for source path %q", name, sp.String())
	if mapping, exists := pm.mappings[sp.String()]; exists && mapping.Xattrs != nil {
		delete(mapping.Xattrs, name)
		if len(mapping.Xattrs) == 0 {
			mapping.Xattrs = nil
		}
		pm.mappings[sp.String()] = mapping
	}
}

// ListXattrs lists all extended attributes for a source path
func (pm *PathMapper) ListXattrs(sp *SourcePath) ([]string, bool) {
	mapping, exists := pm.mappings[sp.String()]
	if !exists || mapping.Xattrs == nil {
		pm.logger.Trace("No xattrs to list for source path: %q", sp.String())
		return nil, false
	}
	attrs := make([]string, 0, len(mapping.Xattrs))
	for name := range mapping.Xattrs {
		attrs = append(attrs, name)
	}
	pm.logger.Trace("Listing %d xattrs for source path %q", len(attrs), sp.String())
	return attrs, true
}

// UnmappedSourcePaths returns all source paths that don't have virtual mappings
func (pm *PathMapper) UnmappedSourcePaths() []*SourcePath {
	pm.logger.Debug("Finding unmapped source paths")

	var unmapped []*SourcePath
	if err := filepath.Walk(pm.sourceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			pm.logger.Error("Error walking path %q: %v", path, err)
			return err
		}
		if path == pm.sourceRoot {
			return nil
		}
		relPath, err := filepath.Rel(pm.sourceRoot, path)
		if err != nil {
			pm.logger.Error("Error getting relative path for %q: %v", path, err)
			return err
		}
		sp := NewSourcePath(relPath)
		if mapping, exists := pm.mappings[sp.String()]; !exists || mapping.VirtualPath == "" {
			pm.logger.Trace("Found unmapped path: %q", sp.String())
			unmapped = append(unmapped, sp)
		}
		return nil
	}); err != nil {
		pm.logger.Error("Failed to walk source directory: %v", err)
		return nil
	}

	pm.logger.Debug("Found %d unmapped paths", len(unmapped))
	return unmapped
}
