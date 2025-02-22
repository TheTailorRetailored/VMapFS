package main

import (
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"vmapfs/internal/fs"
	"vmapfs/internal/logging"
	"vmapfs/internal/state"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
)

var (
	logger = logging.GetLogger()
)

func main() {
	// Parse command line flags
	mountPoint := flag.String("mount", "", "Mount point for virtual filesystem")
	sourcePath := flag.String("source", "", "Source directory to map")
	stateFile := flag.String("state", "", "State file path (required)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	// Configure logging based on flags
	if *verbose {
		logger.SetLevel(logging.LevelDebug)
	}

	logger.Info("Starting VMapFS...")
	logger.Debug("Mount point: %s", *mountPoint)
	logger.Debug("Source path: %s", *sourcePath)
	logger.Debug("State file: %s", *stateFile)

	if *mountPoint == "" || *sourcePath == "" || *stateFile == "" {
		logger.Error("Mount point, source path, and state file path are required")
		os.Exit(1)
	}

	cleanMount := filepath.Clean(*mountPoint)
	cleanSource := filepath.Clean(*sourcePath)

	logger.Info("Initializing state manager...")
	stateManager, err := state.NewManager(*stateFile)
	if err != nil {
		logger.Error("Failed to initialize state manager: %v", err)
		os.Exit(1)
	}

	fsState, err := stateManager.LoadState()
	if err != nil {
		logger.Error("Failed to load state: %v", err)
		os.Exit(1)
	}

	logger.Info("Creating virtual filesystem...")
	vfs, err := fs.NewVMapFS(cleanSource, fsState, stateManager)
	if err != nil {
		logger.Error("Failed to create virtual filesystem: %v", err)
		os.Exit(1)
	}

	logger.Debug("Setting up signal handlers...")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("Mounting filesystem...")
	c, err := fuse.Mount(cleanMount,
		fuse.FSName("vmapfs"),
		fuse.Subtype("vmapfs"),
		fuse.AllowOther(),
		fuse.DefaultPermissions(),
	)
	if err != nil {
		logger.Error("Mount failed: %v", err)
		os.Exit(1)
	}
	defer c.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	logger.Debug("Starting FUSE server...")
	go func() {
		defer wg.Done()
		logger.Info("Serving filesystem...")
		if err := fusefs.Serve(c, vfs); err != nil {
			logger.Error("FUSE server error: %v", err)
		}
		logger.Debug("FUSE server stopped")
	}()

	logger.Info("Filesystem mounted and ready")

	// Wait for signal
	go func() {
		sig := <-sigChan
		logger.Info("Received signal %v", sig)
		if err := fuse.Unmount(cleanMount); err != nil {
			logger.Error("Unmount error: %v", err)
		}
	}()

	wg.Wait()
	logger.Info("Clean shutdown complete")
}
