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

import "context"

// runPreflightFn is the function called by Cmd's RunE to execute pre-flight
// steps. It is a variable so tests can substitute a stub that returns an
// error, exercising the error-propagation path without a real Pulumi/auth
// integration.
var runPreflightFn = runPreflight

// runPreflight resolves runtime configuration from external sources (e.g.
// Pulumi stack outputs, authenticated user context) and returns a map of
// environment variables to inject into every service process.
//
// This is intentionally a stub. Callers that want offline operation should
// pass skip=true, which short-circuits the function and returns an empty map.
func runPreflight(_ context.Context, skip bool) (map[string]string, error) {
	if skip {
		return nil, nil
	}

	// TODO: query Pulumi stack outputs and authenticated user context, then
	// return the resolved values as key/value pairs (e.g. OTEL_EXPORTER_OTLP_ENDPOINT).
	// If resolution fails, return a descriptive error so the caller can surface
	// "run `dr auth login` first" or similar guidance.
	return nil, nil
}
