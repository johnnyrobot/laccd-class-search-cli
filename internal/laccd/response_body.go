// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package laccd

import (
	"fmt"
	"io"
)

const maxResponseBodyBytes int64 = 8 << 20

// readBoundedBody reads at most limit+1 bytes so callers can distinguish an
// exactly-at-limit response from an oversized response. Read failures are
// returned even when the reader produced a partial body.
func readBoundedBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response body exceeds %d-byte limit", limit)
	}
	return data, nil
}
