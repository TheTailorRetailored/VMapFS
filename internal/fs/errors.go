// Package fs provides filesystem implementations.
//
// This file contains error types and error handling utilities.
package fs

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"vmapfs/internal/logging"
)

var (
	errLogger = logging.GetLogger().WithPrefix("error")

	// ErrPathNotFound indicates a virtual path doesn't exist
	ErrPathNotFound = errors.New("virtual path not found")

	// ErrInvalidPath indicates an invalid path format
	ErrInvalidPath = errors.New("invalid path format")

	// ErrReadOnly indicates attempt to modify read-only filesystem
	ErrReadOnly = errors.New("filesystem is read-only")

	// ErrDirectoryNotEmpty indicates attempt to remove non-empty directory
	ErrDirectoryNotEmpty = errors.New("directory not empty")

	// ErrAlreadyExists indicates path already exists
	ErrAlreadyExists = errors.New("path already exists")
)

// FSError (renamed to Error because of linter) wraps filesystem
// errors with context about the operation and affected path to
//
//	provide more detailed error information.
type Error struct {
	Op   string // Operation that failed (e.g., "lookup", "readdir")
	Path string // Affected path
	Err  error  // Underlying error
}

// Error implements the error interface, providing a formatted error message
func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("operation %s failed: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("operation %s on %s failed: %v", e.Op, e.Path, e.Err)
}

// Unwrap implements error unwrapping for the errors.Is/As functions
func (e *Error) Unwrap() error {
	return e.Err
}

// ToFuseError converts a FSError to appropriate FUSE error code.
// This is used to translate our internal errors into the correct
// syscall errors that FUSE expects.
func ToFuseError(err error) error {
	if err == nil {
		return nil
	}

	var fsErr *Error
	if errors.As(err, &fsErr) {
		errLogger.Trace("Converting FSError to FUSE error: %v", fsErr)

		switch {
		case errors.Is(fsErr.Err, ErrPathNotFound):
			return syscall.ENOENT
		case errors.Is(fsErr.Err, ErrInvalidPath):
			return syscall.EINVAL
		case errors.Is(fsErr.Err, ErrReadOnly):
			return syscall.EROFS
		case errors.Is(fsErr.Err, ErrDirectoryNotEmpty):
			return syscall.ENOTEMPTY
		case errors.Is(fsErr.Err, ErrAlreadyExists):
			return syscall.EEXIST
		default:
			errLogger.Debug("Unknown FSError type, returning EIO: %v", fsErr)
			return syscall.EIO
		}
	}

	// For non-FSErrors, convert common error types
	errLogger.Trace("Converting standard error to FUSE error: %v", err)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, os.ErrPermission):
		return syscall.EACCES
	default:
		errLogger.Debug("Unknown error type, returning EIO: %v", err)
		return syscall.EIO
	}
}

// NewFSError creates a new FSError with the given operation, path, and underlying error
func NewFSError(op string, path string, err error) *Error {
	fsErr := &Error{
		Op:   op,
		Path: path,
		Err:  err,
	}
	errLogger.Debug("Created new FSError: %v", fsErr)
	return fsErr
}

// Common operation names for consistent logging and error reporting
const (
	OpLookup  = "lookup"  // Looking up a path
	OpReadDir = "readdir" // Reading directory contents
	OpOpen    = "open"    // Opening a file
	OpRead    = "read"    // Reading from a file
	OpCreate  = "create"  // Creating a new file
	OpMkdir   = "mkdir"   // Creating a new directory
	OpRemove  = "remove"  // Removing a file or directory
	OpRename  = "rename"  // Renaming/moving a file or directory
	OpSetattr = "setattr" // Setting file attributes
	OpGetattr = "getattr" // Getting file attributes
)

// IsTemporary returns true if the error is likely temporary and the
// operation could succeed if retried. This is used to handle transient
// errors appropriately.
func IsTemporary(err error) bool {
	var fsErr *Error
	if errors.As(err, &fsErr) {
		errLogger.Trace("Checking if FSError is temporary: %v", fsErr)
		return false // Our internal errors are not temporary
	}

	// Check for common temporary errors
	errLogger.Trace("Checking if standard error is temporary: %v", err)
	switch {
	case errors.Is(err, syscall.EAGAIN):
		return true
	case errors.Is(err, syscall.EBUSY):
		return true
	case errors.Is(err, syscall.ETIMEDOUT):
		return true
	default:
		return false
	}
}
