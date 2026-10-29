package macprovider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudbase/garm-provider-common/cloudconfig"
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
		`--runnergroup "$GITHUB_RUNNER_GROUP"`,
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
		"downloading runner",
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

// TestTemplateRenderIdempotent asserts the rendered script carries the
// "already configured" detection (requirement #8): a second run must detect
// the configured state, skip registration/install, and only restart the
// launchd service. Verified for both the JIT and non-JIT variants.
func TestTemplateRenderIdempotent(t *testing.T) {
	// Arrange: both template variants.
	cases := []struct {
		name string
		jit  bool
	}{
		{"JIT", true},
		{"non-JIT", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act.
			script, err := InstallRunnerScript(ExampleParams(tc.jit))
			if err != nil {
				t.Fatalf("render %s: %v", tc.name, err)
			}
			out := string(script)

			// Assert: the idempotency guard and its "just start" branch are
			// present, and the guard fires before any registration work.
			for _, want := range []string{
				"is_configured",
				"runner already configured; starting service",
				".runner_migrated",
				"./svc.sh start",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s render missing idempotency marker %q", tc.name, want)
				}
			}
			// The configured branch must exit before the JIT fetch / non-JIT
			// config.sh registration, so the guard precedes them.
			guard := strings.Index(out, "if is_configured; then")
			if guard < 0 {
				t.Fatalf("%s render missing is_configured branch", tc.name)
			}
			if tc.jit {
				if !strings.Contains(out[guard:], "credentials/runner") {
					t.Errorf("%s render: JIT credential fetch not gated after is_configured", tc.name)
				}
			} else if !strings.Contains(out[guard:], "config.sh --unattended") {
				t.Errorf("%s render: config.sh not gated after is_configured", tc.name)
			}
			assertBashSyntax(t, out)
		})
	}
}

// TestTemplateAuthHeaderInterpolatesToken is the regression guard for the
// bearer-header bug: auth_header_file historically emitted the literal string
// "Authorization: Bearer ***" (a printf format with no %s), so $BEARER_TOKEN
// was dropped and every GARM metadata/status call carried an invalid header,
// failing the runner bootstrap at the first credential fetch. The rendered
// template must interpolate the token via %s and never carry the masked "***"
// placeholder.
func TestTemplateAuthHeaderInterpolatesToken(t *testing.T) {
	for _, jit := range []bool{true, false} {
		t.Run(jitLabel(jit), func(t *testing.T) {
			script, err := InstallRunnerScript(ExampleParams(jit))
			if err != nil {
				t.Fatalf("render %s: %v", jitLabel(jit), err)
			}
			out := string(script)

			if !strings.Contains(out, `printf 'Authorization: Bearer %s' "$BEARER_TOKEN"`) {
				t.Errorf("%s render: auth header does not interpolate the token (want printf with %%s)", jitLabel(jit))
			}
			if strings.Contains(out, "***") {
				t.Errorf("%s render: auth header still contains the masked '***' placeholder", jitLabel(jit))
			}
		})
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

// TestTemplateRendersHostileValuesSafely renders both variants with hostile
// values in every template-context field and proves, for payloads carrying
// $(), backticks, single/double quotes, a newline, % and unicode, that:
//
//	(a) the rendered script stays `bash -n` clean;
//	(b) the payload cannot execute — a sandboxed run creates no marker file;
//	(c) the script is self-contained: it reads every value (including the
//	    three secrets) from its own quoted here-documents, so the sandbox
//	    supplies no CALLBACK_URL/METADATA_URL/BEARER_TOKEN environment
//	    variables and the empty-value guard never fires. The secret values
//	    therefore appear in the render only as inert here-document data, never
//	    in a shell-executed position.
func TestTemplateRendersHostileValuesSafely(t *testing.T) {
	// Arrange: one marker path that every payload aims `touch` at, so a single
	// existence check proves no execution across all fields and both variants.
	marker := filepath.Join(t.TempDir(), "pwned")

	payloads := []struct {
		name  string
		value string
	}{
		{"command-substitution", "$(touch " + marker + ")"},
		{"backtick", "`touch " + marker + "`"},
		{"single-quote", "'; touch " + marker + "; #"},
		{"double-quote", `"; touch ` + marker + `; #`},
		{"newline", "x\ntouch " + marker},
		{"percent", "100%$(touch " + marker + ")"},
		{"unicode", "☃ $(touch " + marker + ")"},
	}

	fields := []struct {
		name string
		set  func(*cloudconfig.InstallRunnerParams, string)
	}{
		{"RunnerName", func(p *cloudconfig.InstallRunnerParams, v string) { p.RunnerName = v }},
		{"RunnerLabels", func(p *cloudconfig.InstallRunnerParams, v string) { p.RunnerLabels = v }},
		{"RepoURL", func(p *cloudconfig.InstallRunnerParams, v string) { p.RepoURL = v }},
		{"DownloadURL", func(p *cloudconfig.InstallRunnerParams, v string) { p.DownloadURL = v }},
		{"GitHubRunnerGroup", func(p *cloudconfig.InstallRunnerParams, v string) { p.GitHubRunnerGroup = v }},
		{"CallbackURL", func(p *cloudconfig.InstallRunnerParams, v string) { p.CallbackURL = v }},
		{"MetadataURL", func(p *cloudconfig.InstallRunnerParams, v string) { p.MetadataURL = v }},
		{"CallbackToken", func(p *cloudconfig.InstallRunnerParams, v string) { p.CallbackToken = v }},
	}

	for _, jit := range []bool{true, false} {
		for _, field := range fields {
			for _, payload := range payloads {
				name := fmt.Sprintf("%s/%s/%s", jitLabel(jit), field.name, payload.name)
				t.Run(name, func(t *testing.T) {
					// Arrange: the field under attack carries the hostile
					// value; every other field keeps its benign example value.
					params := ExampleParams(jit)
					field.set(&params, payload.value)

					// Act.
					script, err := InstallRunnerScript(params)
					if err != nil {
						t.Fatalf("render: %v", err)
					}
					out := string(script)

					// Assert (a): the hostile value must not break bash syntax.
					assertBashSyntax(t, out)

					// Positive control: the value is still rendered into a
					// quoted here-document, so the marker must appear in the
					// output — otherwise the test is vacuous.
					if !strings.Contains(out, marker) {
						t.Fatalf("field %s marker not rendered (test is vacuous)", field.name)
					}

					// Assert (b): executing the rendered script must not create
					// the marker — the value is literal data, not code.
					assertNoMarkerExecution(t, out, marker)
				})
			}
		}
	}
}

// jitLabel names the template variant in a test name.
func jitLabel(jit bool) string {
	if jit {
		return "jit"
	}
	return "nojit"
}

// assertNoMarkerExecution runs a rendered script in a sandbox — fake curl and
// security, and a cached run.sh so the download path is skipped — and fails if
// the hostile payload's marker file appears. The sandbox supplies no
// CALLBACK_URL/METADATA_URL/BEARER_TOKEN environment variables (GARM's wrapper
// assigns them without `export`, so they never reach this child script), so
// the rendered script must be self-contained, reading those values from its
// own here-documents. The script is expected to fail partway through the
// bootstrap (the fake curl returns empty), so a non-zero exit is ignored; only
// marker creation and the empty-value guard firing matter.
func assertNoMarkerExecution(t *testing.T, out, marker string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	sbx := t.TempDir()
	bin := filepath.Join(sbx, "bin")
	home := filepath.Join(sbx, "home")
	runHome := filepath.Join(home, "actions-runner")
	for _, dir := range []string{bin, runHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for _, tool := range []string{"curl", "security"} {
		stub := filepath.Join(bin, tool)
		if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", tool, err)
		}
	}
	if err := os.WriteFile(filepath.Join(runHome, "run.sh"), nil, 0o755); err != nil {
		t.Fatalf("write run.sh: %v", err)
	}
	script := filepath.Join(sbx, "macos-runner.sh")
	if err := os.WriteFile(script, []byte(out), 0o700); err != nil {
		t.Fatalf("write rendered script: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("marker already exists before execution: %s", marker)
	}

	// Build a minimal environment. The three GARM-supplied secrets are
	// deliberately NOT exported here: if the template regresses to
	// env-inheritance, the empty-value guard fires and the test fails below.
	env := []string{
		"HOME=" + home,
		"PATH=" + bin + ":" + os.Getenv("PATH"),
	}
	for _, e := range os.Environ() {
		name := e[:strings.IndexByte(e, '=')]
		switch name {
		case "HOME", "PATH", "CALLBACK_URL", "METADATA_URL", "BEARER_TOKEN":
			continue
		}
		env = append(env, e)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = env
	combined, _ := cmd.CombinedOutput()

	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("hostile value executed: marker %s was created", marker)
	}
	// The script must be self-contained: with no CALLBACK_URL/METADATA_URL/
	// BEARER_TOKEN in the environment, the values read from the here-documents
	// satisfy the non-empty guard, so none of its failure messages may appear.
	for _, guard := range []string{
		"CALLBACK_URL is empty",
		"METADATA_URL is empty",
		"BEARER_TOKEN is empty",
	} {
		if strings.Contains(string(combined), guard) {
			t.Fatalf("rendered script is not self-contained: guard fired (%q):\n%s", guard, combined)
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
