package fs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	// Test File Xattr Operations (moved before FileRename)
	t.Run("FileXattrOperations", func(t *testing.T) {
		root, _ := vfs.Root()
		mappedDir, err := root.(*Dir).Lookup(ctx, "mapped")
		if err != nil {
			t.Fatalf("Failed to lookup mapped directory: %v", err)
		}

		fileNode, err := mappedDir.(*Dir).Lookup(ctx, "testfile.txt")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}

		file := fileNode.(*File)

		// Test Setxattr
		setReq := &fuse.SetxattrRequest{
			Name:  "test.key",
			Xattr: []byte("test value"),
		}
		if err := file.Setxattr(ctx, setReq); err != nil {
			t.Errorf("Failed to set xattr: %v", err)
		}

		// Test Getxattr
		getReq := &fuse.GetxattrRequest{Name: "test.key"}
		getResp := &fuse.GetxattrResponse{}
		if err := file.Getxattr(ctx, getReq, getResp); err != nil {
			t.Errorf("Failed to get xattr: %v", err)
		}
		if string(getResp.Xattr) != "test value" {
			t.Errorf("Expected xattr value 'test value', got %q", string(getResp.Xattr))
		}

		// Test Listxattr
		listReq := &fuse.ListxattrRequest{}
		listResp := &fuse.ListxattrResponse{}
		if err := file.Listxattr(ctx, listReq, listResp); err != nil {
			t.Errorf("Failed to list xattrs: %v", err)
		}
		names := strings.Split(string(listResp.Xattr), "\x00")
		found := false
		for _, name := range names {
			if name == "test.key" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected 'test.key' in xattr list, got %v", names)
		}

		// Test Removexattr
		removeReq := &fuse.RemovexattrRequest{Name: "test.key"}
		if err := file.Removexattr(ctx, removeReq); err != nil {
			t.Errorf("Failed to remove xattr: %v", err)
		}
		if err := file.Getxattr(ctx, getReq, getResp); err != fuse.ErrNoXattr {
			t.Errorf("Expected ErrNoXattr after removal, got %v", err)
		}
	})

	// Test file rename (moved after FileXattrOperations)
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
