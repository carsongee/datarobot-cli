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
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

const probeTimeout = 500 * time.Millisecond

// httpProbeClient is reused across all HTTP probe calls to share connection pools.
var httpProbeClient = &http.Client{Timeout: probeTimeout}

// probeTCP dials the loopback address on port and returns true if it connects.
func probeTCP(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// probeHTTP makes a GET to url and returns true if the response status < 500.
func probeHTTP(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := httpProbeClient.Do(req)
	if err != nil {
		return false
	}

	_ = resp.Body.Close()

	return resp.StatusCode < 500
}
