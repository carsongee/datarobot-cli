// Copyright 2026 DataRobot, Inc. and its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !windows

package dev

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// makeCommand creates an exec.Cmd for the service with Unix-specific settings:
// a new process group (Setpgid) so we can SIGTERM the entire process tree,
// and a WaitDelay that escalates to SIGKILL after a grace period.
func makeCommand(ctx context.Context, cfg ServiceConfig) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", cfg.Command)
	cmd.Dir = cfg.Dir
	cmd.Env = buildEnv(cfg.Env)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}

		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return cmd.Process.Signal(syscall.SIGTERM)
		}

		return syscall.Kill(-pgid, syscall.SIGTERM)
	}

	return cmd
}
