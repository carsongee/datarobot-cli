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
	"net/url"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProbeType determines how readiness is checked for a service.
type ProbeType string

const (
	ProbeTCP  ProbeType = "tcp"
	ProbeHTTP ProbeType = "http"
	ProbeNone ProbeType = "none"
)

// ServiceConfig describes a single service to supervise.
type ServiceConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Dir     string            `yaml:"dir"`
	Env     map[string]string `yaml:"env"`
	Port    int               `yaml:"port"`
	Probe   ProbeType         `yaml:"probe"`
	URL     string            `yaml:"url"`
}

// Config holds all service definitions for the dev runner.
type Config struct {
	Services []ServiceConfig `yaml:"services"`
}

// LoadConfig reads, parses, and validates a YAML config file.
// Relative dir paths in service configs are resolved relative to the config
// file's directory, matching the convention of tools like docker-compose.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	var cfg Config

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}

	cfg.normalize()

	// normalize must run before validate so defaults (e.g. HTTP probe URL) are set.
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}

	configDir := filepath.Dir(path)

	for i := range cfg.Services {
		if cfg.Services[i].Dir != "" && !filepath.IsAbs(cfg.Services[i].Dir) {
			cfg.Services[i].Dir = filepath.Join(configDir, cfg.Services[i].Dir)
		}
	}

	return &cfg, nil
}

// normalize sets default values on service configs before validation.
func (c *Config) normalize() {
	for i := range c.Services {
		sc := &c.Services[i]

		if sc.Probe == ProbeHTTP && sc.URL == "" && validPort(sc.Port) {
			sc.URL = fmt.Sprintf("http://localhost:%d", sc.Port)
		}
	}
}

func (c *Config) validate() error {
	if len(c.Services) == 0 {
		return errors.New("config must define at least one service")
	}

	seen := make(map[string]bool, len(c.Services))

	for i := range c.Services {
		sc := &c.Services[i]

		if sc.Name == "" {
			return fmt.Errorf("service[%d]: name is required", i)
		}

		if seen[sc.Name] {
			return fmt.Errorf("duplicate service name %q", sc.Name)
		}

		seen[sc.Name] = true

		if err := sc.validateProbe(); err != nil {
			return err
		}
	}

	return nil
}

func validPort(p int) bool {
	return p >= 1 && p <= 65535
}

// validHTTPURL returns true when rawURL is an absolute http/https URL with a host.
func validHTTPURL(rawURL string) bool {
	u, err := url.Parse(rawURL)

	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (sc *ServiceConfig) validateProbe() error {
	if sc.Command == "" {
		return fmt.Errorf("service %q: command is required", sc.Name)
	}

	switch sc.Probe {
	case ProbeTCP:
		if !validPort(sc.Port) {
			return fmt.Errorf("service %q: probe tcp requires a valid port (1–65535)", sc.Name)
		}
	case ProbeHTTP:
		return sc.validateHTTPProbe()
	case ProbeNone, "":
		// No probe — mark healthy immediately.
	default:
		return fmt.Errorf("service %q: unknown probe type %q (want tcp, http, or none)", sc.Name, sc.Probe)
	}

	return nil
}

func (sc *ServiceConfig) validateHTTPProbe() error {
	if sc.URL == "" && !validPort(sc.Port) {
		return fmt.Errorf("service %q: probe http requires url or a valid port (1–65535)", sc.Name)
	}

	if !validHTTPURL(sc.URL) {
		return fmt.Errorf("service %q: probe http url %q must be an absolute http/https URL", sc.Name, sc.URL)
	}

	return nil
}
