package fs

import (
	"os"
	"path/filepath"
	"strings"

	"vmapfs/internal/logging"
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
// It maintains bidirectional maps to efficiently lookup paths in either direction.
type PathMapper struct {
	virtualToSource map[string]string // virtual path -> source path
	sourceToVirtual map[string]string // source path -> virtual path
	sourceRoot      string
	logger          *logging.Logger
}

// NewPathMapper creates a new PathMapper instance with the given source root
// and initial mappings.
func NewPathMapper(sourceRoot string, initialMappings map[string]string) *PathMapper {
	logger := logging.GetLogger().WithPrefix("pathmap")
	logger.Debug("Creating new path mapper for root: %s", sourceRoot)

	sourceToVirtual := make(map[string]string)
	for vpath, spath := range initialMappings {
		sourceToVirtual[spath] = vpath
		logger.Trace("Initial mapping: %q -> %q", vpath, spath)
	}

	return &PathMapper{
		virtualToSource: initialMappings,
		sourceToVirtual: sourceToVirtual,
		sourceRoot:      sourceRoot,
		logger:          logger,
	}
}

// IsPathMapped returns true if the source path has a virtual mapping
func (pm *PathMapper) IsPathMapped(sp *SourcePath) bool {
	_, exists := pm.sourceToVirtual[sp.String()]
	pm.logger.Trace("Checking if path is mapped: %q (mapped=%v)", sp.String(), exists)
	return exists
}

// GetVirtualPath returns the virtual path for a source path, if one exists
func (pm *PathMapper) GetVirtualPath(sp *SourcePath) (*VirtualPath, bool) {
	vpath, exists := pm.sourceToVirtual[sp.String()]
	pm.logger.Trace("Looking up virtual path: %q -> %q (exists=%v)",
		sp.String(), vpath, exists)
	if !exists {
		return nil, false
	}
	return NewVirtualPath(vpath), true
}

// GetSourcePath returns the source path for a virtual path, if one exists
func (pm *PathMapper) GetSourcePath(vp *VirtualPath) (*SourcePath, bool) {
	spath, exists := pm.virtualToSource[vp.String()]
	pm.logger.Trace("Looking up source path: %q -> %q (exists=%v)",
		vp.String(), spath, exists)
	if !exists {
		return nil, false
	}
	return NewSourcePath(spath), true
}

// AddMapping creates a new virtual->source path mapping
func (pm *PathMapper) AddMapping(vp *VirtualPath, sp *SourcePath) {
	pm.logger.Debug("Adding mapping: %q -> %q", vp.String(), sp.String())
	pm.virtualToSource[vp.String()] = sp.String()
	pm.sourceToVirtual[sp.String()] = vp.String()
}

// RemoveMapping removes a virtual->source path mapping
func (pm *PathMapper) RemoveMapping(vp *VirtualPath) {
	pm.logger.Debug("Removing mapping for: %q", vp.String())
	if spath, exists := pm.virtualToSource[vp.String()]; exists {
		delete(pm.sourceToVirtual, spath)
		delete(pm.virtualToSource, vp.String())
	}
}

// UnmappedSourcePaths returns all source paths that don't have virtual mappings
func (pm *PathMapper) UnmappedSourcePaths() []*SourcePath {
	pm.logger.Debug("Finding unmapped source paths")

	// First get all source paths by walking the source directory
	var unmapped []*SourcePath
	if err := filepath.Walk(pm.sourceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			pm.logger.Error("Error walking path %q: %v", path, err)
			return err
		}

		// Skip the root itself
		if path == pm.sourceRoot {
			return nil
		}

		// Convert to relative path
		relPath, err := filepath.Rel(pm.sourceRoot, path)
		if err != nil {
			pm.logger.Error("Error getting relative path for %q: %v", path, err)
			return err
		}

		sp := NewSourcePath(relPath)
		if !pm.IsPathMapped(sp) {
			pm.logger.Trace("Found unmapped path: %q", sp.String())
			unmapped = append(unmapped, sp)
		}
		return nil
	}); err != nil {
		pm.logger.Error("Failed to walk source directory: %v", err)
		return nil // or return unmapped if you want partial results
	}

	pm.logger.Debug("Found %d unmapped paths", len(unmapped))
	return unmapped
}
