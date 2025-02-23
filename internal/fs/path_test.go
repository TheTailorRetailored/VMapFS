package fs

import (
	"os"
	"path/filepath"
	"testing"
	"vmapfs/internal/state"
)

func TestSourcePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple path",
			input:    "test.txt",
			expected: "test.txt",
		},
		{
			name:     "nested path",
			input:    "dir/test.txt",
			expected: "dir/test.txt",
		},
		{
			name:     "absolute path gets cleaned",
			input:    "/dir/test.txt",
			expected: "dir/test.txt",
		},
		{
			name:     "dot path gets cleaned",
			input:    "./test.txt",
			expected: "test.txt",
		},
		{
			name:     "double dot path gets cleaned",
			input:    "dir/../test.txt",
			expected: "test.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := NewSourcePath(tt.input)
			if sp.String() != tt.expected {
				t.Errorf("Expected path %q, got %q", tt.expected, sp.String())
			}
		})
	}
}

func TestVirtualPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple path",
			input:    "test.txt",
			expected: "/test.txt",
		},
		{
			name:     "nested path",
			input:    "dir/test.txt",
			expected: "/dir/test.txt",
		},
		{
			name:     "already absolute path",
			input:    "/dir/test.txt",
			expected: "/dir/test.txt",
		},
		{
			name:     "dot path gets cleaned",
			input:    "./test.txt",
			expected: "/test.txt",
		},
		{
			name:     "double dot path gets cleaned",
			input:    "dir/../test.txt",
			expected: "/test.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vp := NewVirtualPath(tt.input)
			if vp.String() != tt.expected {
				t.Errorf("Expected path %q, got %q", tt.expected, vp.String())
			}
		})
	}
}

func TestPathMapper(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pathmap-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFiles := []string{
		"file1.txt",
		"dir1/file2.txt",
		"dir1/dir2/file3.txt",
	}

	for _, tf := range testFiles {
		fullPath := filepath.Join(tempDir, tf)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Initialize PathMapper with some initial mappings
	initialMappings := map[string]state.FileMapping{
		"file1.txt":      {VirtualPath: "/mapped/file1.txt"},
		"dir1/file2.txt": {VirtualPath: "/mapped/dir1/file2.txt"},
	}

	pm := NewPathMapper(tempDir, initialMappings)

	t.Run("GetSourcePath", func(t *testing.T) {
		vp := NewVirtualPath("/mapped/file1.txt")
		sp, exists := pm.GetSourcePath(vp)
		if !exists {
			t.Error("Expected path to exist")
		}
		if sp.String() != "file1.txt" {
			t.Errorf("Expected source path %q, got %q", "file1.txt", sp.String())
		}
	})

	t.Run("GetVirtualPath", func(t *testing.T) {
		sp := NewSourcePath("file1.txt")
		vp, exists := pm.GetVirtualPath(sp)
		if !exists {
			t.Error("Expected path to exist")
		}
		if vp.String() != "/mapped/file1.txt" {
			t.Errorf("Expected virtual path %q, got %q", "/mapped/file1.txt", vp.String())
		}
	})

	t.Run("AddMapping", func(t *testing.T) {
		vp := NewVirtualPath("/new/path.txt")
		sp := NewSourcePath("dir1/dir2/file3.txt")
		pm.AddMapping(vp, sp)

		gotSP, exists := pm.GetSourcePath(vp)
		if !exists {
			t.Error("Expected new mapping to exist")
		}
		if gotSP.String() != sp.String() {
			t.Errorf("Expected source path %q, got %q", sp.String(), gotSP.String())
		}
	})

	t.Run("RemoveMapping", func(t *testing.T) {
		vp := NewVirtualPath("/mapped/file1.txt")
		pm.RemoveMapping(vp)

		_, exists := pm.GetSourcePath(vp)
		if exists {
			t.Error("Expected mapping to be removed")
		}
	})

	t.Run("UnmappedSourcePaths", func(t *testing.T) {
		unmapped := pm.UnmappedSourcePaths()

		foundFile1 := false
		for _, sp := range unmapped {
			if sp.String() == "file1.txt" {
				foundFile1 = true
				break
			}
		}

		if !foundFile1 {
			t.Error("Expected file1.txt to be in unmapped paths")
		}
	})
}
