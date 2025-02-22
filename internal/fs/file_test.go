package fs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"bazil.org/fuse"
)

func TestFileOperations(t *testing.T) {
	vfs, sourceDir, _, cleanup := setupTestFS(t)
	defer cleanup()

	ctx := context.Background()

	// Create a test file in the source directory
	testContent := []byte("test file content")
	testFilePath := filepath.Join(sourceDir, "testfile.txt")
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create the mapped directory first
	root, _ := vfs.Root()
	dir := root.(*Dir)

	mappedReq := &fuse.MkdirRequest{Name: "mapped"}
	_, err := dir.Mkdir(ctx, mappedReq)
	if err != nil {
		t.Fatalf("Failed to create mapped directory: %v", err)
	}

	// Add mapping for the test file
	vfs.pathMapper.AddMapping(
		NewVirtualPath("/mapped/testfile.txt"),
		NewSourcePath("testfile.txt"),
	)

	// Test file attributes
	t.Run("FileAttributes", func(t *testing.T) {
		root, _ := vfs.Root()
		mappedDir, err := root.(*Dir).Lookup(ctx, "mapped")
		if err != nil {
			t.Fatalf("Failed to lookup mapped directory: %v", err)
		}

		fileNode, err := mappedDir.(*Dir).Lookup(ctx, "testfile.txt")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}

		attr := &fuse.Attr{}
		if err := fileNode.Attr(ctx, attr); err != nil {
			t.Errorf("Failed to get file attributes: %v", err)
		}

		if attr.Mode&os.ModeDir != 0 {
			t.Error("File should not be a directory")
		}

		if attr.Size != uint64(len(testContent)) {
			t.Errorf("Expected size %d, got %d", len(testContent), attr.Size)
		}
	})

	// Test file reading
	t.Run("FileReading", func(t *testing.T) {
		root, _ := vfs.Root()
		mappedDir, err := root.(*Dir).Lookup(ctx, "mapped")
		if err != nil {
			t.Fatalf("Failed to lookup mapped directory: %v", err)
		}

		fileNode, err := mappedDir.(*Dir).Lookup(ctx, "testfile.txt")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}

		// Open the file
		file := fileNode.(*File)
		handle, err := file.Open(ctx, &fuse.OpenRequest{Flags: fuse.OpenReadOnly}, &fuse.OpenResponse{})
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}

		// Read the content
		fh := handle.(*FileHandle)
		resp := &fuse.ReadResponse{}
		err = fh.Read(ctx, &fuse.ReadRequest{Size: len(testContent)}, resp)
		if err != nil && err != io.EOF {
			t.Fatalf("Failed to read file: %v", err)
		}

		if string(resp.Data) != string(testContent) {
			t.Errorf("Expected content %q, got %q", string(testContent), string(resp.Data))
		}

		// Close the file
		if err := fh.Release(ctx, &fuse.ReleaseRequest{}); err != nil {
			t.Errorf("Failed to close file: %v", err)
		}
	})

	// Test file rename
	t.Run("FileRename", func(t *testing.T) {
		root, _ := vfs.Root()
		mappedDir, err := root.(*Dir).Lookup(ctx, "mapped")
		if err != nil {
			t.Fatalf("Failed to lookup mapped directory: %v", err)
		}

		dir := mappedDir.(*Dir)

		// Create target directory
		targetReq := &fuse.MkdirRequest{Name: "target"}
		targetDir, err := root.(*Dir).Mkdir(ctx, targetReq)
		if err != nil {
			t.Fatalf("Failed to create target directory: %v", err)
		}

		// Rename the file
		renameReq := &fuse.RenameRequest{
			OldName: "testfile.txt",
			NewName: "renamed.txt",
		}
		err = dir.Rename(ctx, renameReq, targetDir)
		if err != nil {
			t.Errorf("Failed to rename file: %v", err)
		}

		// Verify old location doesn't have the file
		_, err = dir.Lookup(ctx, "testfile.txt")
		if err == nil {
			t.Error("Old file location should not exist after rename")
		}

		// Verify new location has the file
		newFile, err := targetDir.(*Dir).Lookup(ctx, "renamed.txt")
		if err != nil {
			t.Error("New file location should exist after rename")
		}
		if newFile == nil {
			t.Error("Renamed file not found at new location")
		}
	})
}
