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

//go:build windows

package up

import (
	"context"
	"os/exec"
	"time"
)

// makeCommand creates an exec.Cmd for the service using cmd /C on Windows.
//
// Known limitation: killing cmd.exe does not automatically terminate its
// child processes. A complete implementation would use a Windows Job Object
// (CreateJobObject + AssignProcessToJobObject + JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
// to ensure the entire process tree is torn down on exit. This is a
// development-tool limitation — orphan processes may linger on forced exit.
func makeCommand(ctx context.Context, cfg ServiceConfig) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd", "/C", cfg.Command)
	cmd.Dir = cfg.Dir
	cmd.Env = buildEnv(cfg.Env)
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}

		return cmd.Process.Kill()
	}

	return cmd
}
