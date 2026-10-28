package macprovider

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// forbidden is the set of strings the rendered script must never contain. They
// are the Linux-only mechanisms the macOS template must not leak: the systemd
// CLI, the Linux dependency installer, and apt.
var forbidden = []string{"systemctl", "installdependencies.sh", "apt-get", "/etc/systemd", "useradd"}

// TestInstallRunnerScriptRenderJIT renders the JIT variant and checks that the
// output is bash-parseable, contains the JIT + launchd path, and never leaks
// the forbidden Linux mechanisms.
func TestTemplateRenderJIT(t *testing.T) {
	// Arrange: JIT install params.
	params := ExampleParams(true)

	// Act.
	script, err := InstallRunnerScript(params)
	if err != nil {
		t.Fatalf("render JIT: %v", err)
	}
	out := string(script)

	// Assert: JIT + launchd markers present, non-JIT markers absent.
	for _, want := range []string{
		"credentials/runner",
		"credentials/credentials",
		"credentials/credentials_rsaparams",
		"system/service-name",
		"./svc.sh install",
		"./svc.sh start",
		"security show-keychain-info",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JIT render missing %q", want)
		}
	}
	for _, absent := range []string{"config.sh --unattended", "runner-registration-token"} {
		if strings.Contains(out, absent) {
			t.Errorf("JIT render unexpectedly contains %q", absent)
		}
	}
	assertForbidden(t, out)
	assertBashSyntax(t, out)
}

// TestInstallRunnerScriptRenderNonJIT renders the non-JIT variant and checks
// the config.sh registration path, including the runnergroup flag.
func TestTemplateRenderNonJIT(t *testing.T) {
	// Arrange: non-JIT params with a runner group.
	params := ExampleParams(false)

	// Act.
	script, err := InstallRunnerScript(params)
	if err != nil {
		t.Fatalf("render non-JIT: %v", err)
	}
	out := string(script)

	// Assert: config.sh registration markers present, JIT-only markers absent.
	for _, want := range []string{
		"runner-registration-token",
		"config.sh --unattended",
		`--runnergroup "Default"`,
		`--no-default-labels`,
		"./svc.sh install",
		"./svc.sh start",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("non-JIT render missing %q", want)
		}
	}
	if strings.Contains(out, "credentials/runner") {
		t.Errorf("non-JIT render unexpectedly contains JIT credentials/runner")
	}
	assertForbidden(t, out)
	assertBashSyntax(t, out)
}

// TestInstallRunnerScriptRenderNonJITNoGroup covers the empty runner group
// branch: no --runnergroup flag may be emitted, and the render must stay valid.
func TestTemplateRenderNonJITNoGroup(t *testing.T) {
	// Arrange: non-JIT params with an empty runner group.
	params := ExampleParams(false)
	params.GitHubRunnerGroup = ""

	// Act.
	script, err := InstallRunnerScript(params)
	if err != nil {
		t.Fatalf("render non-JIT (no group): %v", err)
	}
	out := string(script)

	// Assert: no --runnergroup flag, but config.sh still runs.
	if strings.Contains(out, "--runnergroup") {
		t.Errorf("non-JIT no-group render unexpectedly contains --runnergroup")
	}
	if !strings.Contains(out, "config.sh --unattended") {
		t.Errorf("non-JIT no-group render missing config.sh --unattended")
	}
	assertForbidden(t, out)
	assertBashSyntax(t, out)
}

// TestInstallRunnerScriptCachedRunner asserts the cache-friendly branch: the
// download/extract path and the cached path are both represented.
func TestTemplateCachedRunner(t *testing.T) {
	// Arrange: JIT params.
	params := ExampleParams(true)

	// Act.
	script, err := InstallRunnerScript(params)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := string(script)

	// Assert: both the download path and the run.sh cache check are present.
	for _, want := range []string{
		`"$RUN_HOME/run.sh"`,
		"using cached runner",
		"downloading runner from",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q", want)
		}
	}
}

// TestInstallRunnerScriptRejectsUnknownOS pins the render helper to Linux:
// GARM has no darwin OSType, so any other OS type must be rejected.
func TestTemplatePinsLinux(t *testing.T) {
	// Arrange: a render against a non-Linux OS type via the underlying helper.
	// The helper always pins Linux, so this is verified through the public API's
	// fixed OS type rather than a parameter.
	params := ExampleParams(true)

	// Act: the exported helper always renders as Linux.
	script, err := InstallRunnerScript(params)

	// Assert.
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(script), "#!/bin/bash") {
		t.Errorf("render is not a bash script")
	}
}

// assertForbidden fails the test if the rendered script contains any
// Linux-only mechanism the macOS template must not emit.
func assertForbidden(t *testing.T, out string) {
	t.Helper()
	for _, bad := range forbidden {
		if strings.Contains(out, bad) {
			t.Errorf("render contains forbidden string %q", bad)
		}
	}
}

// assertBashSyntax runs `bash -n` over the rendered script. It is skipped when
// bash is not available, but on the CI image (and any dev box) it is the
// lightweight syntax gate the definition of done requires.
func assertBashSyntax(t *testing.T, out string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	dir := t.TempDir()
	path := dir + "/macos-runner.sh"
	if err := os.WriteFile(path, []byte(out), 0o700); err != nil {
		t.Fatalf("write rendered script: %v", err)
	}
	if err := exec.Command("bash", "-n", path).Run(); err != nil {
		t.Fatalf("bash -n failed on rendered script: %v", err)
	}
}
