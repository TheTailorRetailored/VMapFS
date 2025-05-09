package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"vmapfs/internal/logging"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
)

var (
	unsortedLogger = logging.GetLogger().WithPrefix("unsorted")
)

// UnsortedDir represents the _UNSORTED directory that shows unmapped files
type UnsortedDir struct {
	fs   *VMapFS
	path *SourcePath
}

func NewUnsortedDir(fs *VMapFS, path *SourcePath) *UnsortedDir {
	unsortedLogger.Trace("Creating new UnsortedDir for path: %q", path.String())
	return &UnsortedDir{
		fs:   fs,
		path: path,
	}
}

func (d *UnsortedDir) Attr(_ context.Context, a *fuse.Attr) error {
	unsortedLogger.Trace("Getting attributes for path: %q", d.path.String())

	// If this is the root _UNSORTED dir, return standard attrs
	if d.path.String() == "" {
		a.Mode = os.ModeDir | 0755
		a.Uid = d.fs.uid
		a.Gid = d.fs.gid
		return nil
	}

	// Otherwise get real directory attributes
	info, err := os.Stat(d.path.FullPath(d.fs.sourceDir))
	if err != nil {
		unsortedLogger.Error("Failed to stat directory: %v", err)
		return err
	}

	a.Mode = info.Mode()
	a.Size = uint64(info.Size())
	a.Mtime = info.ModTime()
	a.Atime = info.ModTime()
	a.Ctime = info.ModTime()
	a.Uid = d.fs.uid
	a.Gid = d.fs.gid

	return nil
}

func (d *UnsortedDir) Lookup(_ context.Context, name string) (fusefs.Node, error) {
	unsortedLogger.Debug("Looking up %q in _UNSORTED path %q", name, d.path.String())
	childPath := NewSourcePath(filepath.Join(d.path.String(), name))
	fullPath := childPath.FullPath(d.fs.sourceDir)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			unsortedLogger.Debug("Path not found: %q", fullPath)
			return nil, syscall.ENOENT
		}
		unsortedLogger.Error("Error stating path: %v", err)
		return nil, err
	}

	// Check if path is mapped (only fully mapped with non-empty virtual_path counts)
	if d.fs.pathMapper.IsPathMapped(childPath) {
		unsortedLogger.Debug("Path is already mapped: %q", childPath.String())
		return nil, syscall.ENOENT
	}

	if info.IsDir() {
		if d.isDirectoryEmpty(childPath) {
			unsortedLogger.Debug("Directory is empty of unmapped files: %q", childPath.String())
			return nil, syscall.ENOENT
		}
		unsortedLogger.Debug("Returning directory: %q", childPath.String())
		return NewUnsortedDir(d.fs, childPath), nil
	}

	unsortedLogger.Debug("Returning file: %q", childPath.String())
	return &UnsortedFile{
		fs:   d.fs,
		path: childPath,
	}, nil
}

func (d *UnsortedDir) ReadDirAll(_ context.Context) ([]fuse.Dirent, error) {
	unsortedLogger.Debug("Reading _UNSORTED directory: %q", d.path.String())
	entries, err := os.ReadDir(d.path.FullPath(d.fs.sourceDir))
	if err != nil {
		unsortedLogger.Error("Error reading directory: %v", err)
		return nil, err
	}

	dirEntries := make([]fuse.Dirent, 0, len(entries))
	for _, entry := range entries {
		childPath := NewSourcePath(filepath.Join(d.path.String(), entry.Name()))

		// Skip if fully mapped (non-empty virtual_path)
		if d.fs.pathMapper.IsPathMapped(childPath) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		var entryType fuse.DirentType
		if info.IsDir() {
			entryType = fuse.DT_Dir
			isEmpty := true
			err := filepath.Walk(filepath.Join(d.path.FullPath(d.fs.sourceDir), entry.Name()), func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					relPath, _ := filepath.Rel(d.fs.sourceDir, path)
					sourcePath := NewSourcePath(relPath)
					if !d.fs.pathMapper.IsPathMapped(sourcePath) {
						isEmpty = false
						return filepath.SkipDir
					}
				}
				return nil
			})
			if err != nil {
				unsortedLogger.Error("Error walking directory: %v", err)
				return nil, err
			}
			if isEmpty {
				continue
			}
		} else {
			entryType = fuse.DT_File
		}

		unsortedLogger.Trace("Adding entry: %q (type=%v)", entry.Name(), entryType)
		dirEntries = append(dirEntries, fuse.Dirent{
			Name: entry.Name(),
			Type: entryType,
		})
	}

	unsortedLogger.Debug("Found %d entries in directory", len(dirEntries))
	return dirEntries, nil
}

func (d *UnsortedDir) Rename(_ context.Context, req *fuse.RenameRequest, newDir fusefs.Node) error {
	unsortedLogger.Info("UNSORTED RENAME: from %q/%q", d.path.String(), req.OldName)

	targetDir, ok := newDir.(*Dir)
	if !ok {
		if _, isUnsorted := newDir.(*UnsortedDir); isUnsorted {
			unsortedLogger.Warn("Cannot move within _UNSORTED")
			return syscall.EPERM
		}
		unsortedLogger.Error("Invalid target directory type")
		return syscall.EINVAL
	}

	sourcePath := filepath.Join(d.path.String(), req.OldName)
	sp := NewSourcePath(sourcePath)
	newBasePath := filepath.Join(targetDir.path.String(), req.NewName)
	fullSourcePath := sp.FullPath(d.fs.sourceDir)

	info, err := os.Stat(fullSourcePath)
	if err != nil {
		unsortedLogger.Error("Source not found: %v", err)
		return err
	}

	// If it's a regular file, map directly
	if !info.IsDir() {
		unsortedLogger.Info("Moving file %q -> %q", sp.String(), newBasePath)
		d.fs.mu.Lock()
		d.fs.pathMapper.AddMapping(NewVirtualPath(newBasePath), sp)
		err := d.fs.stateManager.SaveState(d.fs.state)
		d.fs.mu.Unlock()
		if err != nil {
			unsortedLogger.Error("Failed to save state: %v", err)
			return err
		}
		unsortedLogger.Info("File moved successfully")
		return nil
	}

	// If it's a directory, recursively map all child files only
	unsortedLogger.Info("Moving directory %q -> %q (mapping children only)", sp.String(), newBasePath)

	var filesToMap []struct {
		source *SourcePath
		target *VirtualPath
	}

	err = filepath.Walk(fullSourcePath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(d.fs.sourceDir, path)
		if relErr != nil {
			return relErr
		}
		source := NewSourcePath(rel)
		suffix := strings.TrimPrefix(path, fullSourcePath)
		suffix = strings.TrimPrefix(suffix, "/")
		target := NewVirtualPath(filepath.Join(newBasePath, suffix))
		filesToMap = append(filesToMap, struct {
			source *SourcePath
			target *VirtualPath
		}{source, target})
		return nil
	})
	if err != nil {
		unsortedLogger.Error("Failed to walk source directory: %v", err)
		return err
	}

	// Create the parent virtual directory path
	d.fs.mu.Lock()
	d.fs.state.Directories[newBasePath] = true

	for _, pair := range filesToMap {
		unsortedLogger.Debug("Mapping file %q -> %q", pair.source.String(), pair.target.String())
		d.fs.pathMapper.AddMapping(pair.target, pair.source)
	}

	err = d.fs.stateManager.SaveState(d.fs.state)
	d.fs.mu.Unlock()
	if err != nil {
		unsortedLogger.Error("Failed to save mapped children: %v", err)
		return err
	}

	unsortedLogger.Info("Directory contents mapped successfully")
	return nil
}

func (d *UnsortedDir) isDirectoryEmpty(path *SourcePath) bool {
	fullPath := path.FullPath(d.fs.sourceDir)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return true
	}

	for _, entry := range entries {
		childPath := NewSourcePath(path.String() + "/" + entry.Name())

		// If entry is a file and it's not mapped, directory is not empty
		if !entry.IsDir() {
			if !d.fs.pathMapper.IsPathMapped(childPath) {
				return false
			}
			continue
		}

		// If entry is a directory, recursively check if it's empty
		if !d.isDirectoryEmpty(childPath) {
			return false
		}
	}

	return true
}

// UnsortedFile represents a file in the _UNSORTED directory
type UnsortedFile struct {
	fs   *VMapFS
	path *SourcePath
}

func (f *UnsortedFile) Attr(_ context.Context, a *fuse.Attr) error {
	unsortedLogger.Trace("Getting attributes for file: %q", f.path.String())
	info, err := os.Stat(f.path.FullPath(f.fs.sourceDir))
	if err != nil {
		unsortedLogger.Error("Failed to stat file: %v", err)
		return err
	}

	a.Mode = info.Mode()
	a.Size = uint64(info.Size())
	a.Mtime = info.ModTime()
	a.Atime = info.ModTime()
	a.Ctime = info.ModTime()
	a.Uid = f.fs.uid
	a.Gid = f.fs.gid
	a.BlockSize = 4096
	a.Blocks = (uint64(info.Size()) + 511) / 512

	return nil
}

func (f *UnsortedFile) Open(_ context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fusefs.Handle, error) {
	unsortedLogger.Debug("Opening file: %q", f.path.String())
	flags := int(req.Flags)
	if flags&os.O_WRONLY != 0 || flags&os.O_RDWR != 0 {
		unsortedLogger.Warn("Write access attempted on read-only file")
		return nil, syscall.EPERM
	}

	file, err := os.OpenFile(f.path.FullPath(f.fs.sourceDir), flags, 0)
	if err != nil {
		unsortedLogger.Error("Failed to open file: %v", err)
		return nil, err
	}

	resp.Flags |= fuse.OpenDirectIO
	return &FileHandle{file: file}, nil
}

// Getxattr retrieves an extended attribute.
func (f *UnsortedFile) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse) error {
	unsortedLogger.Debug("Getting xattr %q for unsorted file %q", req.Name, f.path.String())
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	attrs, exists := f.fs.pathMapper.GetXattrs(f.path)
	if !exists || attrs == nil {
		unsortedLogger.Trace("No xattrs found for source %q", f.path.String())
		return fuse.ErrNoXattr
	}

	value, exists := attrs[req.Name]
	if !exists {
		unsortedLogger.Trace("Xattr %q not found for source %q", req.Name, f.path.String())
		return fuse.ErrNoXattr
	}

	resp.Xattr = value
	unsortedLogger.Trace("Retrieved xattr %q: %d bytes", req.Name, len(value))
	return nil
}

// Setxattr sets an extended attribute.
func (f *UnsortedFile) Setxattr(ctx context.Context, req *fuse.SetxattrRequest) error {
	unsortedLogger.Debug("Setting xattr %q for unsorted file %q (size: %d bytes)", req.Name, f.path.String(), len(req.Xattr))
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	value := make([]byte, len(req.Xattr))
	copy(value, req.Xattr)
	f.fs.pathMapper.SetXattr(f.path, req.Name, value)

	if err := f.fs.stateManager.SaveState(f.fs.state); err != nil {
		unsortedLogger.Error("Failed to save state after setting xattr: %v", err)
		return err
	}

	unsortedLogger.Trace("Xattr %q set successfully", req.Name)
	return nil
}

// Listxattr lists all extended attributes.
func (f *UnsortedFile) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse) error {
	unsortedLogger.Debug("Listing xattrs for unsorted file %q", f.path.String())
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	attrs, exists := f.fs.pathMapper.ListXattrs(f.path)
	if !exists || len(attrs) == 0 {
		unsortedLogger.Trace("No xattrs to list for source %q", f.path.String())
		return nil
	}

	for _, name := range attrs {
		resp.Append(name)
	}

	unsortedLogger.Trace("Listed %d xattrs", len(attrs))
	return nil
}

// Removexattr removes an extended attribute.
func (f *UnsortedFile) Removexattr(ctx context.Context, req *fuse.RemovexattrRequest) error {
	unsortedLogger.Debug("Removing xattr %q for unsorted file %q", req.Name, f.path.String())
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	attrs, exists := f.fs.pathMapper.GetXattrs(f.path)
	if !exists || attrs == nil {
		unsortedLogger.Trace("No xattrs found to remove for source %q", f.path.String())
		return fuse.ErrNoXattr
	}

	if _, exists := attrs[req.Name]; !exists {
		unsortedLogger.Trace("Xattr %q not found for source %q", req.Name, f.path.String())
		return fuse.ErrNoXattr
	}

	f.fs.pathMapper.RemoveXattr(f.path, req.Name)
	if err := f.fs.stateManager.SaveState(f.fs.state); err != nil {
		unsortedLogger.Error("Failed to save state after removing xattr: %v", err)
		return err
	}

	unsortedLogger.Trace("Xattr %q removed successfully", req.Name)
	return nil
}
