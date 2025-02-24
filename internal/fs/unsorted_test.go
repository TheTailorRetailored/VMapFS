package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"bazil.org/fuse"
)

func TestUnsortedDirectory(t *testing.T) {
	vfs, sourceDir, _, cleanup := setupTestFS(t)
	defer cleanup()

	ctx := context.Background()

	// Create some test files in source directory
	testFiles := []string{
		"unsorted1.txt",
		"unsorted2.txt",
		"nested/unsorted3.txt",
		"partial.txt",
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

	// Simulate partial mapping for "partial.txt" (xattrs but no virtual_path)
	vfs.pathMapper.SetXattr(NewSourcePath("partial.txt"), "test.partial", []byte("partial value"))

	// Test _UNSORTED directory listing
	t.Run("UnsortedListing", func(t *testing.T) {
		root, _ := vfs.Root()
		unsortedNode, err := root.(*Dir).Lookup(ctx, "_UNSORTED")
		if err != nil {
			t.Fatalf("Failed to lookup _UNSORTED: %v", err)
		}

		unsorted := unsortedNode.(*UnsortedDir)
		entries, err := unsorted.ReadDirAll(ctx)
		if err != nil {
			t.Fatalf("Failed to read _UNSORTED directory: %v", err)
		}

		foundFiles := make(map[string]bool)
		foundDirs := make(map[string]bool)
		for _, entry := range entries {
			if entry.Type == fuse.DT_File {
				foundFiles[entry.Name] = true
			} else if entry.Type == fuse.DT_Dir {
				foundDirs[entry.Name] = true
			}
		}

		if !foundFiles["unsorted1.txt"] {
			t.Error("Expected to find unsorted1.txt in _UNSORTED root")
		}
		if !foundFiles["unsorted2.txt"] {
			t.Error("Expected to find unsorted2.txt in _UNSORTED root")
		}
		if !foundDirs["nested"] {
			t.Error("Expected to find 'nested' directory in _UNSORTED root")
		}
		if !foundFiles["partial.txt"] {
			t.Error("Expected to find partial.txt (no virtual_path) in _UNSORTED root")
		}

		if foundDirs["nested"] {
			nestedNode, err := unsorted.Lookup(ctx, "nested")
			if err != nil {
				t.Fatalf("Failed to lookup nested directory: %v", err)
			}

			nestedEntries, err := nestedNode.(*UnsortedDir).ReadDirAll(ctx)
			if err != nil {
				t.Fatalf("Failed to read nested directory: %v", err)
			}

			foundNested := false
			for _, entry := range nestedEntries {
				if entry.Name == "unsorted3.txt" {
					foundNested = true
					break
				}
			}

			if !foundNested {
				t.Error("Expected to find unsorted3.txt in nested directory")
			}
		}
	})

	// Test moving file from _UNSORTED to virtual path
	t.Run("MoveFromUnsorted", func(t *testing.T) {
		root, _ := vfs.Root()

		// Create target directory
		targetReq := &fuse.MkdirRequest{Name: "target"}
		targetDir, err := root.(*Dir).Mkdir(ctx, targetReq)
		if err != nil {
			t.Fatalf("Failed to create target directory: %v", err)
		}

		// Look up _UNSORTED directory
		unsortedNode, err := root.(*Dir).Lookup(ctx, "_UNSORTED")
		if err != nil {
			t.Fatalf("Failed to lookup _UNSORTED: %v", err)
		}

		// Move a file from _UNSORTED to target directory
		renameReq := &fuse.RenameRequest{
			OldName: "unsorted1.txt",
			NewName: "sorted.txt",
		}
		err = unsortedNode.(*UnsortedDir).Rename(ctx, renameReq, targetDir)
		if err != nil {
			t.Errorf("Failed to move file from _UNSORTED: %v", err)
		}

		// Verify file appears in target directory
		movedFile, err := targetDir.(*Dir).Lookup(ctx, "sorted.txt")
		if err != nil {
			t.Error("Moved file should exist in target directory")
		}
		if movedFile == nil {
			t.Error("Moved file not found in target directory")
		}

		// Verify file no longer appears in _UNSORTED
		entries, err := unsortedNode.(*UnsortedDir).ReadDirAll(ctx)
		if err != nil {
			t.Fatalf("Failed to read _UNSORTED directory: %v", err)
		}

		for _, entry := range entries {
			if entry.Name == "unsorted1.txt" {
				t.Error("Moved file should not appear in _UNSORTED")
			}
		}
	})

	// Test that we can't move directories to _UNSORTED
	t.Run("NoDirectoriesInUnsorted", func(t *testing.T) {
		root, _ := vfs.Root()

		// Create a directory in the root
		req := &fuse.MkdirRequest{Name: "testdir"}
		_, err := root.(*Dir).Mkdir(ctx, req)
		if err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}

		// Get _UNSORTED directory
		unsortedNode, err := root.(*Dir).Lookup(ctx, "_UNSORTED")
		if err != nil {
			t.Fatalf("Failed to lookup _UNSORTED: %v", err)
		}

		// Try to use the unsorted directory as target
		renameReq := &fuse.RenameRequest{
			OldName: "testdir",
			NewName: "testdir",
		}
		err = root.(*Dir).Rename(ctx, renameReq, unsortedNode)
		if err != syscall.EPERM {
			t.Errorf("Expected EPERM when moving directory to _UNSORTED, got: %v", err)
		}
	})

	// Test UnsortedFile Xattr Operations
	t.Run("UnsortedFileXattrOperations", func(t *testing.T) {
		root, _ := vfs.Root()
		unsortedNode, err := root.(*Dir).Lookup(ctx, "_UNSORTED")
		if err != nil {
			t.Fatalf("Failed to lookup _UNSORTED: %v", err)
		}

		unsortedFileNode, err := unsortedNode.(*UnsortedDir).Lookup(ctx, "unsorted2.txt")
		if err != nil {
			t.Fatalf("Failed to lookup unsorted file: %v", err)
		}

		unsortedFile := unsortedFileNode.(*UnsortedFile)

		// Test Setxattr
		setReq := &fuse.SetxattrRequest{
			Name:  "test.unsorted.key",
			Xattr: []byte("unsorted test value"),
		}
		if err := unsortedFile.Setxattr(ctx, setReq); err != nil {
			t.Errorf("Failed to set xattr on unsorted file: %v", err)
		}

		// Test Getxattr
		getReq := &fuse.GetxattrRequest{Name: "test.unsorted.key"}
		getResp := &fuse.GetxattrResponse{}
		if err := unsortedFile.Getxattr(ctx, getReq, getResp); err != nil {
			t.Errorf("Failed to get xattr on unsorted file: %v", err)
		}
		if string(getResp.Xattr) != "unsorted test value" {
			t.Errorf("Expected xattr value 'unsorted test value', got %q", string(getResp.Xattr))
		}

		// Test Listxattr
		listReq := &fuse.ListxattrRequest{}
		listResp := &fuse.ListxattrResponse{}
		if err := unsortedFile.Listxattr(ctx, listReq, listResp); err != nil {
			t.Errorf("Failed to list xattrs on unsorted file: %v", err)
		}
		names := strings.Split(string(listResp.Xattr), "\x00")
		found := false
		for _, name := range names {
			if name == "test.unsorted.key" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected 'test.unsorted.key' in xattr list, got %v", names)
		}

		// Test Removexattr
		removeReq := &fuse.RemovexattrRequest{Name: "test.unsorted.key"}
		if err := unsortedFile.Removexattr(ctx, removeReq); err != nil {
			t.Errorf("Failed to remove xattr on unsorted file: %v", err)
		}
		if err := unsortedFile.Getxattr(ctx, getReq, getResp); err != fuse.ErrNoXattr {
			t.Errorf("Expected ErrNoXattr after removal, got %v", err)
		}
	})
}
