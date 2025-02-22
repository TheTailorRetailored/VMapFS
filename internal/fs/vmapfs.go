package fs

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"vmapfs/internal/logging"
	"vmapfs/internal/state"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
)

var (
	vfsLogger = logging.GetLogger().WithPrefix("vfs")
)

// VMapFS represents the core virtual filesystem implementation.
// It manages the mapping between virtual and source paths, handles
// FUSE operations, and maintains filesystem state.
type VMapFS struct {
	sourceDir    string         // Root directory of source files
	state        *state.FSState // Current filesystem state
	stateManager *state.Manager // Manages state persistence
	pathMapper   *PathMapper    // Handles path mapping
	conn         *fuse.Conn     // FUSE connection
	uid          uint32         // User ID for filesystem operations
	gid          uint32         // Group ID for filesystem operations
	mu           sync.RWMutex   // Protects state access
}

// NewVMapFS creates a new virtual filesystem instance.
func NewVMapFS(sourceDir string, state *state.FSState, stateManager *state.Manager) (*VMapFS, error) {
	vfsLogger.Info("Creating new virtual filesystem")
	vfsLogger.Debug("Source directory: %s", sourceDir)

	// Get UID/GID from environment if set
	uid := safeIntToUint32(os.Getuid())
	gid := safeIntToUint32(os.Getgid())

	if puidStr := os.Getenv("PUID"); puidStr != "" {
		if puid, err := strconv.ParseUint(puidStr, 10, 32); err == nil {
			uid = uint32(puid)
			vfsLogger.Debug("Using PUID from environment: %d", uid)
		}
	}
	if pgidStr := os.Getenv("PGID"); pgidStr != "" {
		if pgid, err := strconv.ParseUint(pgidStr, 10, 32); err == nil {
			gid = uint32(pgid)
			vfsLogger.Debug("Using PGID from environment: %d", gid)
		}
	}

	// Initialize path mapper with existing mappings
	vfsLogger.Debug("Initializing path mapper with %d existing mappings",
		len(state.VirtualPaths))
	pathMapper := NewPathMapper(sourceDir, state.VirtualPaths)

	vfs := &VMapFS{
		sourceDir:    sourceDir,
		state:        state,
		stateManager: stateManager,
		pathMapper:   pathMapper,
		uid:          uid,
		gid:          gid,
	}

	vfsLogger.Info("Virtual filesystem created successfully")
	return vfs, nil
}

// Root implements the fusefs.FS interface, returning the root directory node.
func (vfs *VMapFS) Root() (fusefs.Node, error) {
	vfsLogger.Trace("Getting root directory node")
	return &Dir{
		fs:   vfs,
		path: NewVirtualPath("/"),
	}, nil
}

func waitForMount(mountpoint string) error {
	for i := 0; i < 30; i++ {
		info, err := os.Stat(mountpoint)
		if err == nil && info.IsDir() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("mount point not available after 3 seconds")
}

// Mount implements filesystem mounting.
func (vfs *VMapFS) Mount(mountPoint string) error {
	vfsLogger.Info("Mounting virtual filesystem")
	vfsLogger.Debug("Mount point: %s", mountPoint)
	vfsLogger.Debug("Source directory: %s", vfs.sourceDir)
	vfsLogger.Debug("UID: %d, GID: %d", vfs.uid, vfs.gid)

	// Check if source directory is readable
	if _, err := os.ReadDir(vfs.sourceDir); err != nil {
		vfsLogger.Error("Cannot read source directory: %v", err)
		return fmt.Errorf("source directory not readable: %w", err)
	}

	mountOpts := []fuse.MountOption{
		fuse.FSName("vmapfs"),
		fuse.Subtype("vmapfs"),
		fuse.AllowOther(),
		fuse.DefaultPermissions(),
		fuse.AsyncRead(),
		fuse.AllowNonEmptyMount(),
	}

	vfsLogger.Debug("Mounting with options: %+v", mountOpts)

	c, err := fuse.Mount(mountPoint, mountOpts...)
	if err != nil {
		return fmt.Errorf("mount failed: %w", err)
	}
	vfs.conn = c

	// Use context for graceful shutdown
	_, cancel := context.WithCancel(context.Background())
	go func() {
		defer cancel()
		if err := fusefs.Serve(c, vfs); err != nil {
			vfsLogger.Error("FUSE server error: %v", err)
		}
	}()

	// Wait for mount to be ready
	if err := waitForMount(mountPoint); err != nil {
		c.Close()
		vfsLogger.Error("Mount point not ready: %v", err)
		return fmt.Errorf("mount point failed to initialize: %w", err)
	}

	vfsLogger.Info("Filesystem mounted successfully")
	return nil
}

// Unmount cleanly unmounts the filesystem.
func (vfs *VMapFS) Unmount(mountPoint string) error {
	vfsLogger.Info("Unmounting filesystem from: %s", mountPoint)
	if vfs.conn != nil {
		err := fuse.Unmount(mountPoint)
		if err != nil {
			vfsLogger.Error("Unmount failed: %v", err)
		} else {
			vfsLogger.Info("Unmount completed successfully")
		}
		return err
	}
	return nil
}
