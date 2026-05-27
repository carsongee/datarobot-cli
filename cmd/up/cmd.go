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

package up

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/datarobot/cli/internal/cli"
	"github.com/datarobot/cli/internal/features"
	"github.com/datarobot/cli/internal/telemetry"
	"github.com/datarobot/cli/tui"
	"github.com/spf13/cobra"
)

// Options holds command-line flags for `dr up`.
type Options struct {
	ConfigFile    string
	SkipPreflight bool
}

// Cmd returns the cobra.Command for `dr up`.
func Cmd() *cobra.Command {
	var opts Options

	cmd := &cobra.Command{
		Use:     "up",
		GroupID: "core",
		Short:   "Run development services with a live TUI dashboard",
		Long: `Start and supervise local development services with real-time
visibility into process health, resource usage, and log output.

Services are defined in a YAML config file (default: dr-up.yaml).

Before starting services, pre-flight steps may query external sources
(e.g. Pulumi stack outputs, authenticated user context) and inject the
results as environment variables so services start with the right
configuration automatically. Use --skip-preflight to bypass this step.

Example config (dr-up.yaml):

  services:
    - name: api
      command: uv run uvicorn app.main:app --reload
      dir: backend
      port: 8080
      probe: tcp
    - name: frontend
      command: npm run dev
      dir: frontend
      port: 5173
      probe: tcp

Keybindings:
  j / k / ↑↓  Navigate service list
  r            Restart selected service
  s            Stop selected service
  m            Mute / unmute logs for selected service
  /            Filter logs by text (Enter to apply, Esc to clear)
  G            Scroll logs to bottom
  o            Open service URL in browser (http probe URL or localhost:<port>)
  q / Esc      Quit and stop all services`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig(opts.ConfigFile)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Config file %q not found. Create it or use --config to specify a path.\n",
						opts.ConfigFile)

					cmd.SilenceErrors = true

					return cli.ErrSilent
				}

				return fmt.Errorf("loading config: %w", err)
			}

			if len(cfg.Services) == 0 {
				return errors.New("config file defines no services")
			}

			extraEnv, err := runPreflightFn(cmd.Context(), opts.SkipPreflight)
			if err != nil {
				return fmt.Errorf("pre-flight: %w", err)
			}

			m := NewModel(cmd.Context(), cfg, extraEnv)
			defer m.StopAll()

			_, err = tui.Run(m, tea.WithAltScreen(), tea.WithContext(cmd.Context()))
			if err != nil {
				return fmt.Errorf("running TUI: %w", err)
			}

			return nil
		},
	}

	features.SetGate(cmd, "up")

	cmd.Flags().StringVarP(&opts.ConfigFile, "config", "c", "dr-up.yaml",
		"path to service config file")
	cmd.Flags().BoolVar(&opts.SkipPreflight, "skip-preflight", false,
		"skip pre-flight configuration steps (for offline use)")

	telemetry.TrackWith(cmd, func(_ *cobra.Command, _ []string) map[string]any {
		return map[string]any{
			"config_file":    opts.ConfigFile,
			"skip_preflight": opts.SkipPreflight,
		}
	})

	return cmd
}
