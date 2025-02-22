package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vmapfs/internal/state"

	"bazil.org/fuse"
)

func setupTestFS(t *testing.T) (*VMapFS, string, string, func()) {
	// Create temp directories for source and state
	sourceDir, err := os.MkdirTemp("", "vmapfs-source-*")
	if err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	stateDir, err := os.MkdirTemp("", "vmapfs-state-*")
	if err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}

	// Create state manager
	statePath := filepath.Join(stateDir, "state.json")
	stateManager, err := state.NewManager(statePath)
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	// Load initial state
	fsState, err := stateManager.LoadState()
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	// Create virtual filesystem
	vfs, err := NewVMapFS(sourceDir, fsState, stateManager)
	if err != nil {
		t.Fatalf("Failed to create virtual filesystem: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(sourceDir)
		os.RemoveAll(stateDir)
	}

	return vfs, sourceDir, stateDir, cleanup
}

func TestDirOperations(t *testing.T) {
	vfs, sourceDir, _, cleanup := setupTestFS(t)
	defer cleanup()

	// Create some test files in source directory
	testFiles := []string{
		"file1.txt",
		"dir1/file2.txt",
		"dir1/dir2/file3.txt",
	}

	for _, tf := range testFiles {
		fullPath := filepath.Join(sourceDir, tf)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	ctx := context.Background()

	// Test root directory
	t.Run("RootDirectory", func(t *testing.T) {
		root, rootErr := vfs.Root()
		if rootErr != nil {
			t.Fatalf("Failed to get root: %v", rootErr)
		}

		// Check attributes
		attr := &fuse.Attr{}
		if attrErr := root.Attr(ctx, attr); attrErr != nil {
			t.Errorf("Failed to get root attributes: %v", attrErr)
		}
		if attr.Mode&os.ModeDir == 0 {
			t.Error("Root should be a directory")
		}

		// Check directory listing
		dir, ok := root.(*Dir)
		if !ok {
			t.Fatal("Root should be a Dir")
		}

		entries, readErr := dir.ReadDirAll(ctx)
		if readErr != nil {
			t.Errorf("Failed to read root directory: %v", readErr)
		}

		// Root should always have _UNSORTED
		foundUnsorted := false
		for _, entry := range entries {
			if entry.Name == "_UNSORTED" {
				foundUnsorted = true
				break
			}
		}
		if !foundUnsorted {
			t.Error("Root directory should contain _UNSORTED")
		}
	})

	// Test directory creation
	t.Run("CreateDirectory", func(t *testing.T) {
		root, _ := vfs.Root()
		dir := root.(*Dir)

		// Create a new directory
		req := &fuse.MkdirRequest{Name: "newdir"}
		newDir, mkdirErr := dir.Mkdir(ctx, req)
		if mkdirErr != nil {
			t.Fatalf("Failed to create directory: %v", mkdirErr)
		}

		// Verify the returned directory
		if newDir == nil {
			t.Fatal("Mkdir returned nil directory")
		}

		dirAttr := &fuse.Attr{}
		if attrErr := newDir.Attr(ctx, dirAttr); attrErr != nil {
			t.Errorf("Failed to get new directory attributes: %v", attrErr)
		}
		if dirAttr.Mode&os.ModeDir == 0 {
			t.Error("Created node should be a directory")
		}

		// Verify the directory exists
		foundDir, findErr := dir.Lookup(ctx, "newdir")
		if findErr != nil {
			t.Errorf("Failed to lookup new directory: %v", findErr)
		}
		if foundDir == nil {
			t.Error("Created directory not found")
		}

		// Verify directory attributes
		attr := &fuse.Attr{}
		if foundAttrErr := foundDir.Attr(ctx, attr); foundAttrErr != nil {
			t.Errorf("Failed to get directory attributes: %v", foundAttrErr)
		}
		if attr.Mode&os.ModeDir == 0 {
			t.Error("Created node should be a directory")
		}
	})

	// Test nested directory creation
	t.Run("CreateNestedDirectory", func(t *testing.T) {
		root, _ := vfs.Root()
		dir := root.(*Dir)

		// Create parent directory
		req1 := &fuse.MkdirRequest{Name: "parent"}
		parentDir, err := dir.Mkdir(ctx, req1)
		if err != nil {
			t.Fatalf("Failed to create parent directory: %v", err)
		}

		// Create child directory
		req2 := &fuse.MkdirRequest{Name: "child"}
		_, err = parentDir.(*Dir).Mkdir(ctx, req2)
		if err != nil {
			t.Fatalf("Failed to create child directory: %v", err)
		}

		// Verify the nested structure
		found, err := dir.Lookup(ctx, "parent")
		if err != nil {
			t.Errorf("Failed to lookup parent directory: %v", err)
		}

		childDir, err := found.(*Dir).Lookup(ctx, "child")
		if err != nil {
			t.Errorf("Failed to lookup child directory: %v", err)
		}
		if childDir == nil {
			t.Error("Child directory not found")
		}
	})

	// Test directory removal
	t.Run("RemoveDirectory", func(t *testing.T) {
		rmdirCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		root, _ := vfs.Root()
		dir := root.(*Dir)

		// Create a directory
		req := &fuse.MkdirRequest{Name: "todelete"}
		_, err := dir.Mkdir(rmdirCtx, req)
		if err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}

		// Add logging
		t.Log("Directory created, attempting removal")

		// Remove the directory
		removeReq := &fuse.RemoveRequest{Name: "todelete", Dir: true}
		err = dir.Remove(rmdirCtx, removeReq)
		if err != nil {
			t.Fatalf("Failed to remove directory: %v", err)
		}

		t.Log("Directory removed, verifying")

		// Verify directory is gone
		_, err = dir.Lookup(rmdirCtx, "todelete")
		if err == nil {
			t.Error("Directory should not exist after removal")
		}
	})

	// Test directory rename
	t.Run("RenameDirectory", func(t *testing.T) {
		root, _ := vfs.Root()
		dir := root.(*Dir)

		// Create a directory
		req := &fuse.MkdirRequest{Name: "olddirname"}
		_, err := dir.Mkdir(ctx, req)
		if err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}

		// Create target directory
		targetReq := &fuse.MkdirRequest{Name: "targetdir"}
		targetDir, err := dir.Mkdir(ctx, targetReq)
		if err != nil {
			t.Fatalf("Failed to create target directory: %v", err)
		}

		// Rename the directory
		renameReq := &fuse.RenameRequest{
			OldName: "olddirname",
			NewName: "newdirname",
		}
		err = dir.Rename(ctx, renameReq, targetDir)
		if err != nil {
			t.Errorf("Failed to rename directory: %v", err)
		}

		// Verify old name is gone and new name exists
		_, err = dir.Lookup(ctx, "olddirname")
		if err == nil {
			t.Error("Old directory name should not exist after rename")
		}

		found, err := targetDir.(*Dir).Lookup(ctx, "newdirname")
		if err != nil {
			t.Error("New directory name should exist after rename")
		}
		if found == nil {
			t.Error("Renamed directory not found at new location")
		}
	})
}
