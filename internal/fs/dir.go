package fs

import (
	"context"
	"os"
	"strings"
	"syscall"

	"vmapfs/internal/logging"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
)

var (
	dirLogger = logging.GetLogger().WithPrefix("dir")
)

// Dir represents a directory in the virtual filesystem.
// It can be either the root directory, a virtual directory created by the user,
// or a mapped directory from the source filesystem.
type Dir struct {
	fs   *VMapFS
	path *VirtualPath
}

// Attr implements the Node interface, returning directory attributes.
func (d *Dir) Attr(_ context.Context, a *fuse.Attr) error {
	dirLogger.Trace("Getting attributes for directory: %q", d.path.String())

	// Root directory has special handling
	if d.path.IsRoot() {
		dirLogger.Trace("Setting root directory attributes")
		a.Mode = os.ModeDir | 0755
		a.Uid = d.fs.uid
		a.Gid = d.fs.gid
		return nil
	}

	// Regular virtual directory
	dirLogger.Trace("Setting virtual directory attributes")
	a.Mode = os.ModeDir | 0755
	a.Uid = d.fs.uid
	a.Gid = d.fs.gid

	return nil
}

// Lookup implements the NodeStringLookuper interface, finding a child node.
func (d *Dir) Lookup(_ context.Context, name string) (fusefs.Node, error) {
	dirLogger.Debug("Looking up %q in directory %q", name, d.path.String())
	childPath := NewVirtualPath(d.path.String() + "/" + name)

	// Check if this is the _UNSORTED directory
	if d.path.IsRoot() && name == "_UNSORTED" {
		dirLogger.Debug("Returning UnsortedDir for _UNSORTED")
		return NewUnsortedDir(d.fs, NewSourcePath("")), nil
	}

	// Check if it's a virtual directory
	if _, exists := d.fs.state.Directories[childPath.String()]; exists {
		dirLogger.Debug("Found virtual directory: %q", childPath.String())
		return &Dir{fs: d.fs, path: childPath}, nil
	}

	// Check if it's a mapped file
	if sourcePath, exists := d.fs.pathMapper.GetSourcePath(childPath); exists {
		dirLogger.Debug("Found mapped file: %q -> %q", childPath.String(), sourcePath.String())
		return &File{
			fs:         d.fs,
			path:       childPath,
			sourcePath: sourcePath,
		}, nil
	}

	dirLogger.Debug("Path not found: %q", childPath.String())
	return nil, syscall.ENOENT
}

// ReadDirAll implements the HandleReadDirAller interface, listing directory contents.
func (d *Dir) ReadDirAll(_ context.Context) ([]fuse.Dirent, error) {
	dirLogger.Debug("Reading directory contents: %q", d.path.String())
	var entries []fuse.Dirent

	// Add standard entries
	entries = append(entries, fuse.Dirent{Name: ".", Type: fuse.DT_Dir})
	entries = append(entries, fuse.Dirent{Name: "..", Type: fuse.DT_Dir})

	// For root, add _UNSORTED
	if d.path.IsRoot() {
		dirLogger.Trace("Adding _UNSORTED to root directory listing")
		entries = append(entries, fuse.Dirent{
			Name: "_UNSORTED",
			Type: fuse.DT_Dir,
		})
	}

	// Add virtual subdirectories
	prefix := d.path.String() + "/"
	if d.path.IsRoot() {
		prefix = "/"
	}

	d.fs.mu.RLock()
	dirLogger.Trace("Scanning for virtual directories with prefix: %q", prefix)
	for dirPath := range d.fs.state.Directories {
		if dirPath == "/" {
			continue
		}
		if strings.HasPrefix(dirPath, prefix) {
			relPath := strings.TrimPrefix(dirPath, prefix)
			if !strings.Contains(relPath, "/") {
				dirLogger.Trace("Found virtual directory: %q", relPath)
				entries = append(entries, fuse.Dirent{
					Name: relPath,
					Type: fuse.DT_Dir,
				})
			}
		}
	}

	// Add files mapped to this directory
	dirLogger.Trace("Scanning for mapped files with prefix: %q", prefix)
	for vpath := range d.fs.pathMapper.virtualToSource {
		if strings.HasPrefix(vpath, prefix) {
			relPath := strings.TrimPrefix(vpath, prefix)
			if !strings.Contains(relPath, "/") {
				dirLogger.Trace("Found mapped file: %q", relPath)
				entries = append(entries, fuse.Dirent{
					Name: relPath,
					Type: fuse.DT_File,
				})
			}
		}
	}
	d.fs.mu.RUnlock()

	dirLogger.Debug("Directory %q contains %d entries", d.path.String(), len(entries))
	return entries, nil
}

// Mkdir implements the NodeMkdirer interface, creating a new virtual directory.
func (d *Dir) Mkdir(_ context.Context, req *fuse.MkdirRequest) (fusefs.Node, error) {
	dirLogger.Info("Creating new directory %q in %q", req.Name, d.path.String())

	// Create new virtual path
	newPath := NewVirtualPath(d.path.String() + "/" + req.Name)

	// Check if this is an attempt to mkdir in _UNSORTED
	if _, isUnsorted := d.fs.pathMapper.GetSourcePath(d.path); isUnsorted {
		dirLogger.Warn("Attempted to create directory in _UNSORTED: %s", newPath.String())
		return nil, syscall.EPERM
	}

	// Add to state
	d.fs.mu.Lock()
	d.fs.state.Directories[newPath.String()] = true

	// Save state immediately
	err := d.fs.stateManager.SaveState(d.fs.state)
	d.fs.mu.Unlock()

	if err != nil {
		dirLogger.Error("Failed to save state after mkdir: %v", err)
		return nil, err
	}

	dirLogger.Info("Successfully created directory: %s", newPath.String())
	return &Dir{fs: d.fs, path: newPath}, nil
}

// Remove implements the NodeRemover interface, removing a file or directory.
func (d *Dir) Remove(_ context.Context, req *fuse.RemoveRequest) error {
	dirLogger.Info("Removing %q from directory %q (isDir=%v)",
		req.Name, d.path.String(), req.Dir)

	childPath := NewVirtualPath(d.path.String() + "/" + req.Name)

	// First check if we can remove it
	d.fs.mu.RLock()
	if req.Dir {
		// Check if directory exists
		if _, exists := d.fs.state.Directories[childPath.String()]; !exists {
			d.fs.mu.RUnlock()
			dirLogger.Warn("Directory not found: %q", childPath.String())
			return syscall.ENOENT
		}

		// Check if directory is empty
		prefix := childPath.String() + "/"
		for vpath := range d.fs.pathMapper.virtualToSource {
			if strings.HasPrefix(vpath, prefix) {
				d.fs.mu.RUnlock()
				dirLogger.Warn("Directory not empty: %q", childPath.String())
				return syscall.ENOTEMPTY
			}
		}
	}
	d.fs.mu.RUnlock()

	// Now do the actual removal
	d.fs.mu.Lock()
	if req.Dir {
		dirLogger.Debug("Removing directory: %q", childPath.String())
		delete(d.fs.state.Directories, childPath.String())
	} else {
		dirLogger.Debug("Removing file mapping: %q", childPath.String())
		d.fs.pathMapper.RemoveMapping(childPath)
	}

	// Save state
	err := d.fs.stateManager.SaveState(d.fs.state)
	d.fs.mu.Unlock()

	if err != nil {
		dirLogger.Error("Failed to save state: %v", err)
		return err
	}

	dirLogger.Info("Successfully removed %q", childPath.String())
	return nil
}

// Rename implements the NodeRenamer interface, renaming/moving a file or directory.
func (d *Dir) Rename(_ context.Context, req *fuse.RenameRequest, newDir fusefs.Node) error {
	dirLogger.Info("Renaming %q to %q", req.OldName, req.NewName)

	// Get the target path - handle both Dir and UnsortedDir cases
	var targetPath string
	switch target := newDir.(type) {
	case *Dir:
		targetPath = target.path.String()
	case *UnsortedDir:
		dirLogger.Warn("Cannot move to _UNSORTED directory")
		return syscall.EPERM
	default:
		dirLogger.Error("Target is not a valid directory type")
		return syscall.EINVAL
	}

	oldPath := NewVirtualPath(d.path.String() + "/" + req.OldName)
	newPath := NewVirtualPath(targetPath + "/" + req.NewName)

	dirLogger.Debug("Rename operation: %q -> %q", oldPath.String(), newPath.String())

	d.fs.mu.Lock()
	defer d.fs.mu.Unlock()

	// Check if this is a directory rename
	if _, isDir := d.fs.state.Directories[oldPath.String()]; isDir {
		dirLogger.Debug("Moving directory from %q to %q", oldPath.String(), newPath.String())

		// Remove old directory entry
		delete(d.fs.state.Directories, oldPath.String())

		// Add new directory entry
		d.fs.state.Directories[newPath.String()] = true

		// Update any file mappings under this directory
		oldPrefix := oldPath.String() + "/"
		newPrefix := newPath.String() + "/"

		// Create temporary maps for the new mappings
		newVirtualToSource := make(map[string]string)
		newSourceToVirtual := make(map[string]string)

		// Copy existing mappings that aren't being moved
		for vpath, spath := range d.fs.pathMapper.virtualToSource {
			if !strings.HasPrefix(vpath, oldPrefix) {
				newVirtualToSource[vpath] = spath
				newSourceToVirtual[spath] = vpath
			}
		}

		// Add updated mappings for moved files
		for vpath, spath := range d.fs.pathMapper.virtualToSource {
			if strings.HasPrefix(vpath, oldPrefix) {
				newVpath := newPrefix + strings.TrimPrefix(vpath, oldPrefix)
				dirLogger.Trace("Updating mapping: %q -> %q", vpath, newVpath)
				newVirtualToSource[newVpath] = spath
				newSourceToVirtual[spath] = newVpath
			}
		}

		// Update the path mapper with new mappings
		d.fs.pathMapper.virtualToSource = newVirtualToSource
		d.fs.pathMapper.sourceToVirtual = newSourceToVirtual

	} else {
		// This is a file rename
		sourcePath, exists := d.fs.pathMapper.GetSourcePath(oldPath)
		if !exists {
			dirLogger.Warn("Source path not found: %q", oldPath.String())
			return syscall.ENOENT
		}

		dirLogger.Debug("Moving file from %q to %q", oldPath.String(), newPath.String())

		d.fs.pathMapper.RemoveMapping(oldPath)
		d.fs.pathMapper.AddMapping(newPath, sourcePath)
	}

	err := d.fs.stateManager.SaveState(d.fs.state)
	if err != nil {
		dirLogger.Error("Failed to save state: %v", err)
		return err
	}

	dirLogger.Info("Successfully completed rename operation")
	return nil
}
