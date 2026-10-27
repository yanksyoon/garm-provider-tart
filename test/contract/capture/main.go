// Command capture drives the real GARM external-provider code
// (github.com/cloudbase/garm/runner/providers/external) against a recording
// shim, for all seven operations, and writes the exact environment, stdin and
// expected stdout into a fixture directory keyed by GARM ref. The fixtures pin
// the GARM external-provider contract so the replay test and the nightly
// canary can detect upstream drift before a user does.
//
// Build against a specific GARM ref by resolving github.com/cloudbase/garm to
// that version (a module require or a `replace` to a local checkout); the tool
// records the ref and commit it is told via flags.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm/config"
	garmParams "github.com/cloudbase/garm/params"
	"github.com/cloudbase/garm/runner/common"
	"github.com/cloudbase/garm/runner/providers/external"
)

// operations are the seven GARM external-provider commands, in GARM's own
// command-string spelling. The fixture files are keyed by these exact strings.
var operations = []string{
	"CreateInstance",
	"DeleteInstance",
	"GetInstance",
	"ListInstances",
	"RemoveAllInstances",
	"StartInstance",
	"StopInstance",
}

// requiredStdoutFields is the subset of ProviderInstance fields the contract
// requires every Create/Get response to carry. It is asserted by the replay
// test and pinned in shape.json so a change to it is visible in the diff.
var requiredStdoutFields = []string{
	"addresses",
	"name",
	"os_arch",
	"os_type",
	"provider_id",
	"status",
}

const (
	controllerID = "contract-controller-1"
	poolID       = "9dcf590a-1192-4a9c-b3e4-e0902974c2c0"
	instanceID   = "mac-mini-01/garm-contract-000"
	instanceName = "garm-contract-000"
)

func main() {
	var (
		garmRef          = flag.String("ref", "unknown", "GARM ref (tag or sha) to name the fixture directory and manifest")
		garmCommit       = flag.String("commit", "", "full upstream commit sha of the GARM build being captured")
		outDir           = flag.String("out", "", "fixture output directory (must be empty or non-existent)")
		interfaceVersion = flag.String("interface-version", "v0.1.1", "GARM external-provider interface version to pin")
	)
	flag.Parse()

	if *outDir == "" {
		fatal("missing required -out <fixture-dir>")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("mkdir %s: %v", *outDir, err)
	}

	// Build the recording shim with the scratch output dir baked in, then drive
	// GARM against it. The shim records env+stdin and returns canned output.
	scratch, err := os.MkdirTemp("", "garm-contract-capture-")
	if err != nil {
		fatal("mktemp: %v", err)
	}
	defer os.RemoveAll(scratch)

	shimPath := filepath.Join(scratch, "shim")
	buildShim(shimPath, scratch)

	// The capture must be deterministic: GetEnvironmentVariables() forwards any
	// proxy vars present in the process environment, so scrub them before GARM
	// reads os.Environ(). We intentionally exercise no configured
	// environment_variables — those are operator config, not the contract.
	for _, v := range []string{"http_proxy", "https_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		_ = os.Unsetenv(v)
	}

	extraSpecs := json.RawMessage(`{"cpu":8,"mem":16384,"disk":50}`)

	cfg := &config.Provider{
		Name:         "contract-capture",
		ProviderType: garmParams.ExternalProvider,
		External: config.External{
			InterfaceVersion:   *interfaceVersion,
			ConfigFile:         "/etc/garm/contract-provider.toml",
			ProviderExecutable: shimPath,
		},
	}

	ctx := context.Background()
	provider, err := external.NewProvider(ctx, cfg, controllerID)
	if err != nil {
		fatal("construct garm external provider: %v", err)
	}

	base := common.ProviderBaseParams{
		PoolInfo:       garmParams.Pool{ID: poolID, ExtraSpecs: extraSpecs},
		ControllerInfo: garmParams.ControllerInfo{},
		EntityType:     garmParams.ForgeEntityTypeRepository,
		EntityID:       "contract-repo",
	}

	run := map[string]func() error{
		"CreateInstance": func() error {
			_, err := provider.CreateInstance(ctx, bootstrapParams(extraSpecs), common.CreateInstanceParams{
				CreateInstanceV011: common.CreateInstanceV011Params{ProviderBaseParams: base},
			})
			return err
		},
		"DeleteInstance": func() error {
			return provider.DeleteInstance(ctx, instanceID, common.DeleteInstanceParams{
				DeleteInstanceV011: common.DeleteInstanceV011Params{ProviderBaseParams: base},
			})
		},
		"GetInstance": func() error {
			_, err := provider.GetInstance(ctx, instanceID, common.GetInstanceParams{
				GetInstanceV011: common.GetInstanceV011Params{ProviderBaseParams: base},
			})
			return err
		},
		"ListInstances": func() error {
			_, err := provider.ListInstances(ctx, poolID, common.ListInstancesParams{
				ListInstancesV011: common.ListInstancesV011Params{ProviderBaseParams: base},
			})
			return err
		},
		"RemoveAllInstances": func() error {
			return provider.RemoveAllInstances(ctx, common.RemoveAllInstancesParams{
				RemoveAllInstancesV011: common.RemoveAllInstancesV011Params{ProviderBaseParams: base},
			})
		},
		"StartInstance": func() error {
			return provider.Start(ctx, instanceID, common.StartParams{
				StartV011: common.StartV011Params{ProviderBaseParams: base},
			})
		},
		"StopInstance": func() error {
			return provider.Stop(ctx, instanceID, common.StopParams{
				StopV011: common.StopV011Params{ProviderBaseParams: base},
			})
		},
	}

	shape := shapeDoc{
		GARMVersions: garmVersions(garmRef, garmCommit),
		Operations:   make(map[string]opShape, len(operations)),
	}

	for _, op := range operations {
		// Every operation must round-trip: the shim's canned output has to pass
		// GARM's parse + ValidateResult, proving the expected stdout is valid.
		if err := run[op](); err != nil {
			fatal("drive %s: %v", op, err)
		}
		os, err := readOpShape(scratch, op)
		if err != nil {
			fatal("shape %s: %v", op, err)
		}
		shape.Operations[op] = os
		writeFixture(*outDir, scratch, op)
	}

	if err := writeJSON(filepath.Join(*outDir, "shape.json"), shape); err != nil {
		fatal("write shape.json: %v", err)
	}
	if err := writeJSON(filepath.Join(*outDir, "manifest.json"), manifest{
		GARMRef:          *garmRef,
		GARMCommit:       shape.GARMVersions.Commit,
		InterfaceVersion: *interfaceVersion,
	}); err != nil {
		fatal("write manifest.json: %v", err)
	}

	fmt.Printf("captured GARM external-provider contract for %s (%s) into %s\n",
		shape.GARMVersions.Ref, shape.GARMVersions.Commit, *outDir)
}

// bootstrapParams builds a representative BootstrapInstance covering every field
// GARM can send, so the captured stdin pins the full key shape.
func bootstrapParams(extraSpecs json.RawMessage) commonParams.BootstrapInstance {
	osx := "osx"
	arm64 := "arm64"
	downloadURL := "https://github.com/actions/runner/releases/download/v2.322.0/actions-runner-osx-arm64-2.322.0.tar.gz"
	filename := "actions-runner-osx-arm64-2.322.0.tar.gz"
	sha := "contract-sha256"
	return commonParams.BootstrapInstance{
		Name: instanceName,
		Tools: []commonParams.RunnerApplicationDownload{{
			OS:                &osx,
			Architecture:      &arm64,
			DownloadURL:       &downloadURL,
			Filename:          &filename,
			TempDownloadToken: nil,
			SHA256Checksum:    &sha,
		}},
		RepoURL:           "https://github.com/cloudbase/garm",
		CallbackURL:       "http://garm:9997/api/v1/callbacks/status/",
		MetadataURL:       "http://garm:9997/api/v1/metadata/",
		InstanceToken:     "contract-instance-token",
		SSHKeys:           []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIcontract"},
		ExtraSpecs:        extraSpecs,
		GitHubRunnerGroup: "Default",
		CACertBundle:      []byte("contract-ca-bundle"),
		OSArch:            commonParams.Arm64,
		OSType:            commonParams.Linux,
		Flavor:            "default",
		Image:             "ghcr.io/cirruslabs/macos-tahoe-xcode@sha256:contract",
		Labels:            []string{"self-hosted", "macos", "arm64"},
		PoolID:            poolID,
		UserDataOptions:   commonParams.UserDataOptions{DisableUpdatesOnBoot: true, ExtraPackages: []string{"curl"}, EnableBootDebug: true},
		JitConfigEnabled:  true,
		ProxyConfig:       commonParams.ProxyConfig{HTTPProxy: "http://proxy.example:3128", HTTPSProxy: "http://proxy.example:3128", NoProxy: "localhost"},
	}
}

// buildShim compiles the recording shim with the scratch dir baked in.
func buildShim(shimPath, scratchDir string) {
	modDir := moduleDir()
	cmd := exec.Command("go", "build", "-ldflags", "-X main.outDir="+scratchDir, "-o", shimPath, "./shim")
	cmd.Dir = modDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fatal("build shim: %v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
}

// moduleDir returns the directory holding this source file, so the driver can
// locate ./shim regardless of where it is invoked from (go run builds to a
// temp binary, so os.Executable() is not the source location).
func moduleDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

// readOpShape loads the shim's recorded env, stdin and stdout for one operation
// and reduces them to a value-free shape: env var names, JSON keys, output kind
// and exit code. Values (ids, names, addresses) are deliberately not compared.
func readOpShape(scratchDir, op string) (opShape, error) {
	envRaw, err := os.ReadFile(filepath.Join(scratchDir, op+".env"))
	if err != nil {
		return opShape{}, err
	}
	stdinRaw, err := os.ReadFile(filepath.Join(scratchDir, op+".stdin"))
	if err != nil {
		return opShape{}, err
	}
	stdoutRaw, err := os.ReadFile(filepath.Join(scratchDir, op+".stdout"))
	if err != nil {
		return opShape{}, err
	}

	os := opShape{
		Env:    envNames(string(envRaw)),
		Exit:   0,
		Stdout: stdoutKind(stdoutRaw),
	}
	if keys, err := jsonKeys(stdinRaw); err != nil {
		return opShape{}, fmt.Errorf("stdin keys: %w", err)
	} else {
		os.StdinKeys = keys
	}
	if keys, err := jsonKeys(stdoutRaw); err != nil {
		return opShape{}, fmt.Errorf("stdout keys: %w", err)
	} else {
		os.StdoutKeys = keys
	}
	return os, nil
}

// writeFixture copies the shim's recorded env/stdin/stdout for one operation
// into the fixture directory, with the env sorted for deterministic diffs.
func writeFixture(outDir, scratchDir, op string) {
	envRaw, err := os.ReadFile(filepath.Join(scratchDir, op+".env"))
	if err != nil {
		fatal("read %s.env: %v", op, err)
	}
	lines := strings.Split(strings.TrimRight(string(envRaw), "\n"), "\n")
	sort.Strings(lines)
	mustCopy(filepath.Join(outDir, op+".env"), []byte(strings.Join(lines, "\n")+"\n"))
	copyFrom(scratchDir, outDir, op+".stdin")
	copyFrom(scratchDir, outDir, op+".stdout")
	mustCopy(filepath.Join(outDir, op+".exit"), []byte("0\n"))
}

func copyFrom(srcDir, dstDir, name string) {
	data, err := os.ReadFile(filepath.Join(srcDir, name))
	if err != nil {
		fatal("read %s: %v", name, err)
	}
	mustCopy(filepath.Join(dstDir, name), data)
}

func mustCopy(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fatal("write %s: %v", path, err)
	}
}

// ---- shape model ----------------------------------------------------------

type shapeDoc struct {
	GARMVersions
	Operations map[string]opShape `json:"operations"`
}

type GARMVersions struct {
	Ref              string `json:"garm_ref"`
	Commit           string `json:"garm_commit"`
	InterfaceVersion string `json:"interface_version"`
	// RequiredStdoutFields is pinned so a change to the required ProviderInstance
	// field set is visible in the canary diff.
	RequiredStdoutFields []string `json:"required_stdout_fields"`
}

type opShape struct {
	Env        []string `json:"env"`
	StdinKeys  []string `json:"stdin_keys"`
	StdoutKeys []string `json:"stdout_keys"`
	Stdout     string   `json:"stdout_kind"`
	Exit       int      `json:"exit"`
}

type manifest struct {
	GARMRef          string `json:"garm_ref"`
	GARMCommit       string `json:"garm_commit"`
	InterfaceVersion string `json:"interface_version"`
}

func garmVersions(ref, commit *string) GARMVersions {
	if *commit == "" {
		*commit = detectCommit()
	}
	if *commit == "" {
		*commit = *ref
	}
	return GARMVersions{
		Ref:                  *ref,
		Commit:               *commit,
		InterfaceVersion:     "v0.1.1",
		RequiredStdoutFields: append([]string(nil), requiredStdoutFields...),
	}
}

// detectCommit best-effort extracts the upstream garm version/revision from the
// build info. For a `replace`d local checkout the module carries its VCS
// revision; for a module version it carries the tag. It returns "" when nothing
// is recorded, in which case the caller falls back to the ref name.
func detectCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, m := range info.Deps {
		if m.Path == "github.com/cloudbase/garm" {
			if m.Replace != nil {
				if m.Replace.Version != "" {
					return m.Replace.Version
				}
				return m.Replace.Path
			}
			return m.Version
		}
	}
	return ""
}

// ---- shape helpers --------------------------------------------------------

// envNames extracts the sorted environment variable names from a raw
// `KEY=VALUE\n...` dump.
func envNames(raw string) []string {
	names := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			names = append(names, line[:i])
		}
	}
	sort.Strings(names)
	return names
}

// stdoutKind classifies the expected stdout: "object", "array" or "empty".
func stdoutKind(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "empty"
	}
	if trimmed[0] == '[' {
		return "array"
	}
	return "object"
}

// jsonKeys returns the sorted, de-duplicated set of JSON keys (dotted paths)
// in a document. Arrays contribute their elements' keys without a path element.
func jsonKeys(raw []byte) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []string{}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	collectKeys(v, "", seen)
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func collectKeys(v any, prefix string, seen map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			seen[key] = true
			collectKeys(sub, key, seen)
		}
	case []any:
		for _, sub := range t {
			collectKeys(sub, prefix, seen)
		}
	}
}

// ---- io -------------------------------------------------------------------

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "capture: "+format+"\n", args...)
	os.Exit(1)
}
