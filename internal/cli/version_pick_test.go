// Copyright 2026 johnnyrobot and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import "testing"

// A plain `go install <module>@vX.Y.Z` never sets the ldflags stamp, so the
// module version recorded in the binary is the only source of truth for it.
func TestPickVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stamped string
		module  string
		want    string
	}{
		{"ldflags stamp wins over module version", "9.9.9", "v1.2.3", "9.9.9"},
		{"go install falls back to module version", defaultVersion, "v1.2.3", "1.2.3"},
		{"devel module version is not a release", defaultVersion, "(devel)", defaultVersion},
		{"absent module version keeps default", defaultVersion, "", defaultVersion},
		{"dirty tree keeps its suffix", defaultVersion, "v1.2.3+dirty", "1.2.3+dirty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickVersion(tc.stamped, tc.module); got != tc.want {
				t.Errorf("pickVersion(%q, %q) = %q, want %q", tc.stamped, tc.module, got, tc.want)
			}
		})
	}
}

// The version command, rootCmd.Version, doctor and agent-context all read the
// package var, so init must leave it non-empty whatever the build path was.
func TestVersionResolvedAtInit(t *testing.T) {
	if version == "" {
		t.Fatal("version is empty after init")
	}
}
