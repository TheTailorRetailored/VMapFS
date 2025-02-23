package fs

import (
	"context"
	"io"
	"os"
	"sync"
	"syscall"

	"vmapfs/internal/logging"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
)

var (
	fileLogger = logging.GetLogger().WithPrefix("file")
)

// File represents a mapped file in the virtual filesystem.
type File struct {
	fs         *VMapFS
	path       *VirtualPath
	sourcePath *SourcePath
	mu         sync.RWMutex
}

// Attr implements the Node interface, returning the file's attributes.
func (f *File) Attr(_ context.Context, a *fuse.Attr) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	fileLogger.Trace("Getting attributes for file: %q (source: %q)",
		f.path.String(), f.sourcePath.String())

	info, err := os.Stat(f.sourcePath.FullPath(f.fs.sourceDir))
	if err != nil {
		if os.IsNotExist(err) {
			fileLogger.Warn("Source file not found: %q", f.sourcePath.String())
			return syscall.ENOENT
		}
		fileLogger.Error("Failed to stat file: %v", err)
		return err
	}

	// Copy file attributes
	a.Mode = info.Mode()
	a.Size = safeInt64ToUint64(info.Size())
	a.Mtime = info.ModTime()
	a.Atime = info.ModTime() // We don't track access time
	a.Ctime = info.ModTime() // We don't track creation time
	a.Uid = f.fs.uid
	a.Gid = f.fs.gid
	a.BlockSize = 4096
	a.Blocks = safeInt64ToUint64((info.Size() + 511) / 512)

	fileLogger.Trace("File attributes: mode=%v, size=%d, mtime=%v",
		a.Mode, a.Size, a.Mtime)
	return nil
}

// Open implements the NodeOpener interface, opening the underlying source file.
func (f *File) Open(_ context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fusefs.Handle, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	flags := int(req.Flags)
	fileLogger.Debug("Opening file %q with flags %v", f.path.String(), flags)

	// Enforce read-only access
	if flags&os.O_WRONLY != 0 || flags&os.O_RDWR != 0 {
		fileLogger.Warn("Attempted write access to read-only file: %q", f.path.String())
		return nil, syscall.EPERM
	}

	file, err := os.OpenFile(f.sourcePath.FullPath(f.fs.sourceDir), flags, 0)
	if err != nil {
		fileLogger.Error("Failed to open file: %v", err)
		return nil, err
	}

	// Enable direct IO for better performance
	resp.Flags |= fuse.OpenDirectIO

	fileLogger.Debug("Successfully opened file %q", f.path.String())
	return &FileHandle{
		file: file,
		path: f.path.String(),
	}, nil
}

// Getxattr implements the NodeGetxattrer interface, retrieving an extended attribute.
func (f *File) Getxattr(ctx context.Context, req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	fileLogger.Debug("Getting xattr %q for file %q (source: %q)", req.Name, f.path.String(), f.sourcePath.String())
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	attrs, exists := f.fs.pathMapper.GetXattrs(f.sourcePath)
	if !exists || attrs == nil {
		fileLogger.Trace("No xattrs found for source %q", f.sourcePath.String())
		return fuse.ErrNoXattr
	}

	value, exists := attrs[req.Name]
	if !exists {
		fileLogger.Trace("Xattr %q not found for source %q", req.Name, f.sourcePath.String())
		return fuse.ErrNoXattr
	}

	resp.Xattr = value
	fileLogger.Trace("Retrieved xattr %q: %d bytes", req.Name, len(value))
	return nil
}

// Setxattr implements the NodeSetxattrer interface, setting an extended attribute.
func (f *File) Setxattr(ctx context.Context, req *fuse.SetxattrRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fileLogger.Debug("Setting xattr %q for file %q (source: %q, size: %d bytes)", req.Name, f.path.String(), f.sourcePath.String(), len(req.Xattr))
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	// Store the xattr value (copy to avoid referencing req.Xattr directly)
	value := make([]byte, len(req.Xattr))
	copy(value, req.Xattr)
	f.fs.pathMapper.SetXattr(f.sourcePath, req.Name, value)

	// Save the updated state
	if err := f.fs.stateManager.SaveState(f.fs.state); err != nil {
		fileLogger.Error("Failed to save state after setting xattr: %v", err)
		return err
	}

	fileLogger.Trace("Xattr %q set successfully", req.Name)
	return nil
}

// Listxattr implements the NodeListxattrer interface, listing all extended attributes.
func (f *File) Listxattr(ctx context.Context, req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	fileLogger.Debug("Listing xattrs for file %q (source: %q)", f.path.String(), f.sourcePath.String())
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	attrs, exists := f.fs.pathMapper.ListXattrs(f.sourcePath)
	if !exists || len(attrs) == 0 {
		fileLogger.Trace("No xattrs to list for source %q", f.sourcePath.String())
		return nil
	}

	for _, name := range attrs {
		resp.Append(name)
	}

	fileLogger.Trace("Listed %d xattrs", len(attrs))
	return nil
}

// Removexattr implements the NodeRemovexattrer interface, removing an extended attribute.
func (f *File) Removexattr(ctx context.Context, req *fuse.RemovexattrRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	fileLogger.Debug("Removing xattr %q for file %q (source: %q)", req.Name, f.path.String(), f.sourcePath.String())
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	attrs, exists := f.fs.pathMapper.GetXattrs(f.sourcePath)
	if !exists || attrs == nil {
		fileLogger.Trace("No xattrs found to remove for source %q", f.sourcePath.String())
		return fuse.ErrNoXattr
	}

	if _, exists := attrs[req.Name]; !exists {
		fileLogger.Trace("Xattr %q not found for source %q", req.Name, f.sourcePath.String())
		return fuse.ErrNoXattr
	}

	f.fs.pathMapper.RemoveXattr(f.sourcePath, req.Name)
	if err := f.fs.stateManager.SaveState(f.fs.state); err != nil {
		fileLogger.Error("Failed to save state after removing xattr: %v", err)
		return err
	}

	fileLogger.Trace("Xattr %q removed successfully", req.Name)
	return nil
}

// FileHandle represents an open file handle.
// It manages access to an open file descriptor from the source filesystem.
type FileHandle struct {
	file *os.File
	path string // For logging purposes
	mu   sync.RWMutex
}

// Read implements the HandleReader interface, reading data from the file.
func (fh *FileHandle) Read(_ context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	fh.mu.RLock()
	defer fh.mu.RUnlock()

	fileLogger.Trace("Reading %d bytes from file %q at offset %d",
		req.Size, fh.path, req.Offset)

	resp.Data = make([]byte, req.Size)
	n, err := fh.file.ReadAt(resp.Data, req.Offset)
	if err != nil && err != io.EOF {
		fileLogger.Error("Failed to read from file: %v", err)
		return err
	}

	resp.Data = resp.Data[:n]
	fileLogger.Trace("Successfully read %d bytes", n)
	return nil
}

// Release implements the HandleReleaser interface, closing the file handle.
func (fh *FileHandle) Release(_ context.Context, _ *fuse.ReleaseRequest) error {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	fileLogger.Debug("Closing file %q", fh.path)
	return fh.file.Close()
}
