// Copyright 2025 DataRobot, Inc. and its affiliates.
// All rights reserved.
// DataRobot, Inc. Confidential.
// This is unpublished proprietary source code of DataRobot, Inc.
// and its affiliates.
// The copyright notice above does not evidence any actual or intended
// publication of such source code.

package state

import (
	"os"
	"path/filepath"
	"time"

	"github.com/datarobot/cli/internal/repo"
	"github.com/datarobot/cli/internal/version"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	stateFileName = "state.yaml"
	cliSubDir     = "cli"
	localStateDir = ".datarobot"
)

// State represents the current state of CLI interactions with a repository.
type State struct {
	// CLIVersion is the version of the CLI used for the successful run
	CLIVersion string `yaml:"cli_version"`
	// LastStart is an ISO8601-compliant timestamp of the last successful `dr start` run
	LastStart time.Time `yaml:"last_start"`
	// LastTemplatesSetup is an ISO8601-compliant timestamp of the last successful `dr templates setup` run
	LastTemplatesSetup *time.Time `yaml:"last_templates_setup,omitempty"`
	// LastDotenvSetup is an ISO8601-compliant timestamp of the last successful `dr dotenv setup` run
	LastDotenvSetup *time.Time `yaml:"last_dotenv_setup,omitempty"`
}

// GetStatePath determines the appropriate location for the state file.
// The state file is stored in .datarobot/cli directory within the repository.
// If repoRoot is empty, it will attempt to find the repository root using FindRepoRoot.
func GetStatePath(repoRoot string) (string, error) {
	var err error

	if repoRoot == "" {
		repoRoot, err = repo.FindRepoRoot()
		if err != nil {
			return "", err
		}
	}

	// Use local .datarobot/cli directory
	localPath := filepath.Join(repoRoot, localStateDir, cliSubDir)
	statePath := filepath.Join(localPath, stateFileName)

	return statePath, nil
}

// Load reads the state file from the appropriate location.
// Returns nil if the file doesn't exist (first run).
// If repoRoot is empty, it will attempt to find the repository root.
func Load(repoRoot string) (*State, error) {
	statePath, err := GetStatePath(repoRoot)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // File doesn't exist yet, not an error
		}

		return nil, err
	}

	var state State

	err = yaml.Unmarshal(data, &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// Update saves the state file and automatically sets the CLIVersion.
// This should be the preferred method for saving state.
// If repoRoot is empty, it will attempt to find the repository root.
func (s *State) Update(repoRoot string) error {
	s.CLIVersion = version.Version

	return Save(s, repoRoot)
}

// Save writes the state file to the appropriate location.
// Creates parent directories if they don't exist.
// Note: Consider using Update() instead, which automatically sets CLIVersion.
// If repoRoot is empty, it will attempt to find the repository root.
func Save(state *State, repoRoot string) error {
	statePath, err := GetStatePath(repoRoot)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	stateDir := filepath.Dir(statePath)

	err = os.MkdirAll(stateDir, 0o755)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return err
	}

	err = os.WriteFile(statePath, data, 0o644)
	if err != nil {
		return err
	}

	return nil
}

// UpdateAfterSuccessfulRun creates or updates the state file after a successful `dr start` run.
// If repoRoot is empty, it will attempt to find the repository root.
func UpdateAfterSuccessfulRun(repoRoot string) error {
	// Load existing state to preserve other fields
	existingState, err := Load(repoRoot)
	if err != nil {
		return err
	}

	if existingState == nil {
		existingState = &State{}
	}

	existingState.LastStart = time.Now().UTC()

	return existingState.Update(repoRoot)
}

// UpdateAfterDotenvSetup updates the state file after a successful `dr dotenv setup` run.
// If repoRoot is empty, it will attempt to find the repository root.
func UpdateAfterDotenvSetup(repoRoot string) error {
	// Load existing state to preserve other fields
	existingState, err := Load(repoRoot)
	if err != nil {
		return err
	}

	if existingState == nil {
		existingState = &State{}
	}

	now := time.Now().UTC()
	existingState.LastDotenvSetup = &now

	return existingState.Update(repoRoot)
}

// UpdateAfterTemplatesSetup updates the state file after a successful `dr templates setup` run.
// If repoRoot is empty, it will attempt to find the repository root.
func UpdateAfterTemplatesSetup(repoRoot string) error {
	// Load existing state to preserve other fields
	existingState, err := Load(repoRoot)
	if err != nil {
		return err
	}

	if existingState == nil {
		existingState = &State{}
	}

	now := time.Now().UTC()
	existingState.LastTemplatesSetup = &now

	return existingState.Update(repoRoot)
}

// HasCompletedDotenvSetup checks if dotenv setup has been completed in the past.
// If force-interactive flag is set, this always returns false to force re-execution.
// If repoRoot is empty, it will attempt to find the repository root.
func HasCompletedDotenvSetup(repoRoot string) bool {
	// Check if we should force the wizard to run
	if viper.GetBool("force-interactive") {
		return false
	}

	state, err := Load(repoRoot)
	if err != nil || state == nil {
		return false
	}

	return state.LastDotenvSetup != nil && state.LastDotenvSetup.Before(time.Now())
}
