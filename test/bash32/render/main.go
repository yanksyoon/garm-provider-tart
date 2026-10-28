// Command render renders the macOS runner-install template exactly as GARM
// does: through cloudconfig.InstallRunnerScript with a fixed
// InstallRunnerParams. It exists so the bash32 test lane exercises the real
// Go text/template semantics (whitespace trim, field interpolation) instead
// of a hand-rolled renderer that could drift from GARM.
//
// Usage:
//
//	render -template <path> [-jit] > rendered.sh
//
// UseJITConfig defaults to true; -non-jit selects the registration-token
// branch. All other params are fixed so the output is deterministic.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cloudbase/garm-provider-common/cloudconfig"
	"github.com/cloudbase/garm-provider-common/params"
)

// Fixed install params. The .invalid hosts are guaranteed-unresolvable so the
// rendered script can never reach the network even if a stub were bypassed;
// the bash32 run.sh substitutes stubs for curl/security/launchctl/svc.sh on
// PATH, so no real binary is ever invoked.
func fixedParams(jit bool) cloudconfig.InstallRunnerParams {
	return cloudconfig.InstallRunnerParams{
		FileName:          "actions-runner-osx-arm64-2.323.0.tar.gz",
		DownloadURL:       "https://downloads.example.invalid/runner.tar.gz",
		RunnerUsername:    "runner",
		RunnerGroup:       "staff",
		RepoURL:           "https://github.com/example/repo",
		MetadataURL:       "http://metadata.invalid",
		RunnerName:        "garm-abc123",
		RunnerLabels:      "self-hosted,macos,arm64",
		CallbackURL:       "http://callback.invalid/status",
		CallbackToken:     "test-callback-token",
		TempDownloadToken: "",
		GitHubRunnerGroup: "",
		EnableBootDebug:   false,
		UseJITConfig:      jit,
	}
}

func main() {
	templatePath := flag.String("template", "", "path to the Go text/template to render")
	jit := flag.Bool("jit", true, "render the JIT branch (UseJITConfig)")
	nonJIT := flag.Bool("non-jit", false, "render the non-JIT branch (registration token)")
	flag.Parse()

	if *templatePath == "" {
		fmt.Fprintln(os.Stderr, "-template is required")
		os.Exit(2)
	}
	if *nonJIT {
		*jit = false
	}

	tpl, err := os.ReadFile(*templatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read template: %v\n", err)
		os.Exit(2)
	}

	out, err := cloudconfig.InstallRunnerScript(fixedParams(*jit), params.Linux, string(tpl))
	if err != nil {
		fmt.Fprintf(os.Stderr, "render template: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "write rendered script: %v\n", err)
		os.Exit(1)
	}
}
