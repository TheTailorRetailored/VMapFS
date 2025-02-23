// Package state provides persistent state management for the virtual filesystem.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"vmapfs/internal/logging"
)

var (
	logger = logging.GetLogger().WithPrefix("state")
)

// Manager handles loading and saving filesystem state
type Manager struct {
	statePath   string
	backupDir   string
	backupCount int
	mu          sync.RWMutex
}

// NewManager creates a new state manager for the given state file path.
// It ensures the state directory exists and is writable.
func NewManager(statePath string) (*Manager, error) {
	logger.Debug("Creating new state manager with path: %s", statePath)

	// Get current working directory
	var firstErr error
	cwd, firstErr := os.Getwd()
	if firstErr != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", firstErr)
	}
	logger.Trace("Current working directory: %s", cwd)

	// Resolve relative to current directory
	absPath := statePath
	if !filepath.IsAbs(statePath) {
		absPath = filepath.Join(cwd, statePath)
	}
	logger.Debug("Resolved state path: %s", absPath)

	// Create parent directory if it doesn't exist
	stateDir := filepath.Dir(absPath)
	logger.Debug("Ensuring state directory exists: %s", stateDir)
	if mkdirErr := os.MkdirAll(stateDir, 0755); mkdirErr != nil {
		return nil, fmt.Errorf("failed to create state directory %s: %w", stateDir, mkdirErr)
	}

	// Try to create an empty file to verify we have write permissions
	f, writeErr := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE, 0644)
	if writeErr != nil {
		return nil, fmt.Errorf("failed to create state file %s: %w", absPath, writeErr)
	}
	f.Close()

	backupDir := filepath.Join(stateDir, ".vmapfs-backups")
	logger.Debug("Creating backup directory: %s", backupDir)
	if backupDirErr := os.MkdirAll(backupDir, 0755); backupDirErr != nil {
		return nil, fmt.Errorf("failed to create backup directory %s: %w", backupDir, backupDirErr)
	}

	logger.Info("State manager initialization complete")
	return &Manager{
		statePath:   absPath,
		backupDir:   backupDir,
		backupCount: 5,
	}, nil
}

// LoadState loads the filesystem state from disk.
// If no state file exists, it creates a new one with default values.
func (sm *Manager) LoadState() (*FSState, error) {
	logger.Debug("Loading state from: %s", sm.statePath)
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if file exists
	info, err := os.Stat(sm.statePath)
	if err != nil || info.Size() == 0 {
		if os.IsNotExist(err) || info.Size() == 0 {
			logger.Info("No valid state file, creating new state")
			state := &FSState{
				Mappings: make(map[string]FileMapping),
				Directories: map[string]bool{
					"/": true,
				},
				Version: 1,
			}

			// Marshal the initial state
			fileData, readErr := json.MarshalIndent(state, "", "  ")
			if readErr != nil {
				return nil, fmt.Errorf("failed to marshal initial state: %w", err)
			}

			// Write initial state
			logger.Debug("Writing initial state file")
			if readErr := os.WriteFile(sm.statePath, fileData, 0600); readErr != nil {
				return nil, fmt.Errorf("failed to write initial state: %w", readErr)
			}

			logger.Info("Created new state file successfully")
			return state, nil
		}
		return nil, fmt.Errorf("failed to check state file: %w", err)
	}

	// Read existing state
	data, err := os.ReadFile(sm.statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("state file is empty")
	}

	logger.Debug("Parsing existing state file (%d bytes)", len(data))
	var state FSState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	// Ensure required fields are initialized
	if state.Mappings == nil {
		state.Mappings = make(map[string]FileMapping)
	}
	if state.Directories == nil {
		state.Directories = make(map[string]bool)
	}
	state.Directories["/"] = true

	logger.Info("State loaded successfully")
	return &state, nil
}

// SaveState saves the current filesystem state to disk.
// It automatically creates a backup before saving.
func (sm *Manager) SaveState(state *FSState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger.Debug("Saving state to: %s", sm.statePath)

	// Create backup before saving
	if backupErr := sm.createBackup(); backupErr != nil {
		logger.Warn("Failed to create backup: %v", backupErr)
		// Continue with save even if backup fails
	}

	// Marshal with indentation for readability
	data, marshalErr := json.MarshalIndent(state, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal state: %w", marshalErr)
	}

	if len(data) == 0 {
		return fmt.Errorf("refusing to write empty state data")
	}

	logger.Trace("Writing %d bytes of state data", len(data))
	if err := os.WriteFile(sm.statePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	// Verify the write
	written, verifyErr := os.ReadFile(sm.statePath)
	if verifyErr != nil {
		return fmt.Errorf("failed to verify written state: %w", verifyErr)
	}
	if len(written) == 0 {
		return fmt.Errorf("state file is empty after write")
	}

	logger.Debug("State saved and verified successfully")
	return nil
}

// createBackup creates a timestamped backup of the current state file
func (sm *Manager) createBackup() error {
	// Skip if state file doesn't exist yet
	if _, err := os.Stat(sm.statePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(sm.statePath)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(sm.backupDir, fmt.Sprintf("state-%s.json", timestamp))

	logger.Debug("Creating backup: %s", backupPath)
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	return sm.cleanupOldBackups()
}

// cleanupOldBackups removes old backup files, keeping only the most recent ones
func (sm *Manager) cleanupOldBackups() error {
	entries, err := os.ReadDir(sm.backupDir)
	if err != nil {
		return err
	}

	type backup struct {
		path    string
		modTime time.Time
	}

	backups := make([]backup, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backup{
				path:    filepath.Join(sm.backupDir, entry.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	// Sort by modification time, newest first
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	// Remove old backups
	for i := sm.backupCount; i < len(backups); i++ {
		logger.Debug("Removing old backup: %s", backups[i].path)
		if err := os.Remove(backups[i].path); err != nil {
			return fmt.Errorf("failed to remove old backup %s: %w", backups[i].path, err)
		}
	}

	return nil
}
