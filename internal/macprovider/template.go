// Package macprovider renders the macOS runner-install template GARM serves to
// Tart guests. The template itself is authored as a Go text/template in
// macos-runner.sh (embedded here); GARM renders it at bootstrap time against a
// cloudconfig.InstallRunnerParams context, and this package exposes the same
// render path so the template can be proven to render — for both the JIT and
// non-JIT variants — without a Mac or a live GARM controller.
package macprovider

import (
	_ "embed"

	"github.com/cloudbase/garm-provider-common/cloudconfig"
	"github.com/cloudbase/garm-provider-common/params"
)

// macosRunnerTemplate is the macOS runner-install template. The pool is created
// with --os-type linux (GARM's params.OSType has no darwin value), so the OS
// type is pinned to Linux even though the rendered script targets macOS.
//
//go:embed macos-runner.sh
var macosRunnerTemplate string

// InstallRunnerScript renders the macOS runner-install template against the
// given install params. It is the Go-side counterpart of what GARM does when it
// serves the template at /install-script/ and is used by the render/dry-run
// scripts and the unit tests.
func InstallRunnerScript(p cloudconfig.InstallRunnerParams) ([]byte, error) {
	return cloudconfig.InstallRunnerScript(p, params.Linux, macosRunnerTemplate)
}

// ExampleParams returns representative install params for the template's two
// variants. The values are stable placeholders chosen to exercise every
// template branch (JIT and non-JIT) without real infrastructure.
func ExampleParams(useJIT bool) cloudconfig.InstallRunnerParams {
	return cloudconfig.InstallRunnerParams{
		FileName:          "actions-runner-osx-arm64-2.322.0.tar.gz",
		DownloadURL:       "https://github.com/actions/runner/releases/download/v2.322.0/actions-runner-osx-arm64-2.322.0.tar.gz",
		RunnerUsername:    "runner",
		RunnerGroup:       "runner",
		RepoURL:           "https://github.com/yanksyoon/garm-provider-tart",
		MetadataURL:       "http://garm.invalid/api/v1/metadata/",
		RunnerName:        "garm-test-runner",
		RunnerLabels:      "self-hosted,macos,arm64",
		CallbackURL:       "http://garm.invalid/api/v1/callbacks/status/",
		CallbackToken:     "test-jwt-token",
		TempDownloadToken: "",
		GitHubRunnerGroup: "Default",
		EnableBootDebug:   false,
		UseJITConfig:      useJIT,
	}
}
