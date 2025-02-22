// internal/fs/interfaces.go

package fs

import (
	"bazil.org/fuse/fs"
)

// Node represents a filesystem node (file or directory)
type Node interface {
	fs.Node
	fs.NodeSetattrer
}

// Directory represents a directory in the virtual filesystem
type Directory interface {
	Node
	fs.NodeStringLookuper
	fs.HandleReadDirAller
	fs.NodeMkdirer
	fs.NodeRemover
	fs.NodeRenamer
}

// FileInterface represents a file in the virtual filesystem
type FileInterface interface {
	Node
	fs.NodeOpener
	fs.NodeFsyncer
}

// FileHandleInterface represents an open file handle
type FileHandleInterface interface {
	fs.Handle
	fs.HandleReader
	fs.HandleReleaser
}
