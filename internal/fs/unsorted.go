package fs

import (
	"context"
	"log"
	"os"
	"path/filepath"
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
	childPath := NewSourcePath(d.path.String() + "/" + name)
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

	// Check if path is already mapped
	if d.fs.pathMapper.IsPathMapped(childPath) {
		unsortedLogger.Debug("Path is already mapped: %q", childPath.String())
		return nil, syscall.ENOENT
	}

	if info.IsDir() {
		// Check if directory is empty of unmapped files
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

		// Skip if path is mapped
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
			// Check if directory has any unmapped files
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
				log.Printf("Error walking directory: %v", err)
				return nil, err
			}

			if !isEmpty {
				entryType = fuse.DT_Dir
			} else {
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
		// If target is also an UnsortedDir, deny the operation
		if _, isUnsorted := newDir.(*UnsortedDir); isUnsorted {
			unsortedLogger.Warn("Cannot move within _UNSORTED")
			return syscall.EPERM
		}
		unsortedLogger.Error("Invalid target directory type")
		return syscall.EINVAL
	}

	// Construct the source path
	sourcePath := filepath.Join(d.path.String(), req.OldName)
	sp := NewSourcePath(sourcePath)
	newPath := NewVirtualPath(filepath.Join(targetDir.path.String(), req.NewName))

	unsortedLogger.Info("Moving %q -> %q", sp.String(), newPath.String())

	// Verify source exists
	fullSourcePath := sp.FullPath(d.fs.sourceDir)
	if _, err := os.Stat(fullSourcePath); err != nil {
		unsortedLogger.Error("Source not found: %v", err)
		return err
	}

	// Create mapping
	d.fs.mu.Lock()
	d.fs.pathMapper.AddMapping(newPath, sp)
	err := d.fs.stateManager.SaveState(d.fs.state)
	d.fs.mu.Unlock()

	if err != nil {
		unsortedLogger.Error("Failed to save state: %v", err)
		return err
	}

	unsortedLogger.Info("Successfully moved file to virtual path")
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
