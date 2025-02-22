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
// It provides access to an underlying file in the source filesystem
// through the virtual filesystem interface.
type File struct {
	fs         *VMapFS
	path       *VirtualPath // Virtual path of the file
	sourcePath *SourcePath  // Actual path in source filesystem
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
