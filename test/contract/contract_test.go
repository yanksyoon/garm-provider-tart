//go:build contract

// Package contract_test pins the GARM external-provider contract and replays
// it against this provider, so a change to the operation names, environment
// variables, BootstrapInstance (stdin) shape or ProviderInstance (stdout) shape
// fails here before a user does.
//
// Two tests:
//
//   - TestReplay runs the real provider binary logic (internal/provider.Run)
//     against each captured fixture and asserts the exit code, stdout JSON
//     validity and that every field GARM requires is present.
//   - TestCanaryDrift diffs a fresh capture (produced by
//     test/contract/capture against upstream GARM main) against the pinned
//     fixtures; any shape drift fails with a readable diff.
//
// Run:
//
//	go test -tags contract ./test/contract/... -count=1 -run TestReplay
package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/yanksyoon/garm-provider-tart/internal/provider"
)

// GARM's exit code for the external provider is 0 on success, non-zero on
// failure. The provider maps unknown commands to exit 2 (usage); the replay
// asserts StartInstance/StopInstance are recognised (anything but 2) because
// their full start/stop semantics land with the bootstrap template (T3).
const (
	exitUsage = 2
	skipNever = "the capture tool and the replay test must stay in lock-step; update both"
)

// operations are the seven GARM external-provider commands, in GARM's own
// spelling (StartInstance/StopInstance, not Start/Stop).
var operations = []string{
	"CreateInstance",
	"DeleteInstance",
	"GetInstance",
	"ListInstances",
	"RemoveAllInstances",
	"StartInstance",
	"StopInstance",
}

// requiredStdoutFields is the subset of ProviderInstance fields every
// Create/Get response must carry, per the GARM contract. It mirrors the value
// pinned in shape.json and is asserted on our provider's output.
var requiredStdoutFields = []string{"provider_id", "name", "os_type", "os_arch", "addresses", "status"}

// ---- shape model (mirrors shape.json written by test/contract/capture) ----

type shapeDoc struct {
	GARMRef              string             `json:"garm_ref"`
	GARMCommit           string             `json:"garm_commit"`
	InterfaceVersion     string             `json:"interface_version"`
	RequiredStdoutFields []string           `json:"required_stdout_fields"`
	Operations           map[string]opShape `json:"operations"`
}

type opShape struct {
	Env        []string `json:"env"`
	StdinKeys  []string `json:"stdin_keys"`
	StdoutKeys []string `json:"stdout_keys"`
	StdoutKind string   `json:"stdout_kind"`
	Exit       int      `json:"exit"`
}

// ---- TestReplay -----------------------------------------------------------

func TestReplay(t *testing.T) {
	for _, refDir := range fixtureDirs(t) {
		ref := filepath.Base(refDir)
		for _, op := range operations {
			t.Run(ref+"/"+op, func(t *testing.T) {
				replayOp(t, refDir, op)
			})
		}
	}
}

// replayOp runs our provider against one captured fixture and asserts the
// contract holds: it reads the exact env names GARM sent, accepts the exact
// stdin, and emits the expected stdout shape with the expected exit code.
func replayOp(t *testing.T, refDir, op string) {
	t.Helper()
	env := readEnvFile(t, filepath.Join(refDir, op+".env"))
	stdin := readFile(t, filepath.Join(refDir, op+".stdin"))
	wantStdout := readFile(t, filepath.Join(refDir, op+".stdout"))
	wantExit := readExit(t, filepath.Join(refDir, op+".exit"))

	// Canned hostctl responses replayed by the ssh stub, routed by verb so the
	// provider's probe-then-create flow completes offline.
	setupStub(t)

	// A fresh provider config whose ssh transport is the test stub, so the
	// provider can run offline and deterministically.
	configPath := writeReplayConfig(t)

	// Feed the provider the exact env names GARM sent, but point
	// GARM_PROVIDER_CONFIG_FILE at the fresh config (the captured value is a
	// path that only existed during capture).
	env["GARM_PROVIDER_CONFIG_FILE"] = configPath

	var stdout, stderr bytes.Buffer
	code := provider.Run(func(k string) string { return env[k] }, bytes.NewReader(stdin), &stdout, &stderr)

	switch op {
	case "CreateInstance", "GetInstance":
		if code != wantExit {
			t.Fatalf("%s exit code = %d, want %d (stderr: %s)", op, code, wantExit, strings.TrimSpace(stderr.String()))
		}
		assertProviderInstanceJSON(t, op, stdout.Bytes(), wantStdout)
	case "ListInstances":
		if code != wantExit {
			t.Fatalf("%s exit code = %d, want %d (stderr: %s)", op, code, wantExit, strings.TrimSpace(stderr.String()))
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
			t.Fatalf("%s stdout is not a JSON array: %v\n%s", op, err, stdout.String())
		}
		if stdout.String() == "null" {
			t.Fatalf("%s stdout is `null`, want a JSON array (never null)", op)
		}
	case "DeleteInstance", "RemoveAllInstances":
		if code != wantExit {
			t.Fatalf("%s exit code = %d, want %d (stderr: %s)", op, code, wantExit, strings.TrimSpace(stderr.String()))
		}
		if got := strings.TrimSpace(stdout.String()); got != "" {
			t.Fatalf("%s stdout = %q, want empty", op, got)
		}
	case "StartInstance", "StopInstance":
		// The contract command name must be recognised (anything but the usage
		// exit). The full boot/shutdown semantics land with the bootstrap
		// template (T3); until then stdout must still be empty so GARM never
		// misparses a half-implemented response.
		if code == exitUsage {
			t.Fatalf("%s not recognised: exit %d (stderr: %s)", op, code, strings.TrimSpace(stderr.String()))
		}
		if got := strings.TrimSpace(stdout.String()); got != "" {
			t.Fatalf("%s stdout = %q, want empty", op, got)
		}
	default:
		t.Fatalf("unhandled operation %q — %s", op, skipNever)
	}
}

// assertProviderInstanceJSON checks the stdout is a valid ProviderInstance
// object, carries every required field, and emits no key GARM's contract does
// not define (compared against the fixture's expected stdout keys).
func assertProviderInstanceJSON(t *testing.T, op string, got, wantStdout []byte) {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("%s stdout is not a JSON object: %v\n%s", op, err, got)
	}

	for _, field := range requiredStdoutFields {
		v, ok := obj[field]
		if !ok {
			t.Fatalf("%s stdout missing required field %q: %s", op, field, got)
		}
		if isEmptyValue(v) {
			t.Fatalf("%s stdout field %q is empty: %s", op, field, got)
		}
	}

	wantKeys := keysOfJSON(t, wantStdout)
	gotKeys := keysOfJSON(t, got)
	for _, k := range gotKeys {
		if !contains(wantKeys, k) {
			t.Fatalf("%s stdout emits field %q which is not in the pinned contract (%v)", op, k, wantKeys)
		}
	}
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	default:
		return false
	}
}

// ---- TestCanaryDrift ------------------------------------------------------

// TestCanaryDrift diffs a fresh capture (GARM_CONTRACT_CAPTURE points at a
// shape.json produced by test/contract/capture against upstream main) against
// the pinned fixtures. It fails with a readable diff on any shape drift and is
// skipped when no capture is provided (so the replay suite runs standalone).
func TestCanaryDrift(t *testing.T) {
	captureDir := os.Getenv("GARM_CONTRACT_CAPTURE")
	if captureDir == "" {
		t.Skip("GARM_CONTRACT_CAPTURE is not set; skipping canary drift check")
	}
	fresh := readShape(t, filepath.Join(captureDir, "shape.json"))

	for _, refDir := range fixtureDirs(t) {
		pinned := readShape(t, filepath.Join(refDir, "shape.json"))
		ref := filepath.Base(refDir)
		t.Run(ref, func(t *testing.T) {
			drifts := diffShapes(pinned, fresh)
			if len(drifts) > 0 {
				t.Fatalf("contract drifted from pinned ref %s (%s):\n%s",
					ref, pinned.GARMCommit, strings.Join(drifts, "\n"))
			}
		})
	}
}

// diffShapes compares the value-free contract shape (env var names, JSON keys,
// output kind, exit code, interface version, required fields) between a pinned
// and a freshly captured shape. Values that legitimately vary (ids, names,
// addresses, commits) are never compared.
func diffShapes(pinned, fresh shapeDoc) []string {
	var diffs []string
	if pinned.InterfaceVersion != fresh.InterfaceVersion {
		diffs = append(diffs, fmt.Sprintf("interface_version: pinned %q != captured %q",
			pinned.InterfaceVersion, fresh.InterfaceVersion))
	}
	if !reflect.DeepEqual(pinned.RequiredStdoutFields, fresh.RequiredStdoutFields) {
		diffs = append(diffs, fmt.Sprintf("required_stdout_fields: pinned %v != captured %v",
			pinned.RequiredStdoutFields, fresh.RequiredStdoutFields))
	}

	for _, op := range sortedOps(unionOps(pinned, fresh)) {
		p, okP := pinned.Operations[op]
		f, okF := fresh.Operations[op]
		switch {
		case !okP && okF:
			diffs = append(diffs, fmt.Sprintf("%s: present in captured, missing from pinned", op))
			continue
		case okP && !okF:
			diffs = append(diffs, fmt.Sprintf("%s: present in pinned, missing from captured", op))
			continue
		case !okP && !okF:
			continue
		}
		if !reflect.DeepEqual(p.Env, f.Env) {
			diffs = append(diffs, fmt.Sprintf("%s.env: pinned %v != captured %v", op, p.Env, f.Env))
		}
		if !reflect.DeepEqual(p.StdinKeys, f.StdinKeys) {
			diffs = append(diffs, fmt.Sprintf("%s.stdin_keys: pinned %v != captured %v", op, p.StdinKeys, f.StdinKeys))
		}
		if !reflect.DeepEqual(p.StdoutKeys, f.StdoutKeys) {
			diffs = append(diffs, fmt.Sprintf("%s.stdout_keys: pinned %v != captured %v", op, p.StdoutKeys, f.StdoutKeys))
		}
		if p.StdoutKind != f.StdoutKind {
			diffs = append(diffs, fmt.Sprintf("%s.stdout_kind: pinned %q != captured %q", op, p.StdoutKind, f.StdoutKind))
		}
		if p.Exit != f.Exit {
			diffs = append(diffs, fmt.Sprintf("%s.exit: pinned %d != captured %d", op, p.Exit, f.Exit))
		}
	}
	return diffs
}

// ---- fixture IO -----------------------------------------------------------

func fixtureDirs(t *testing.T) []string {
	t.Helper()
	root := filepath.Join(contractDir(t), "fixtures")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read fixtures dir %s: %v", root, err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	if len(dirs) == 0 {
		t.Fatalf("no fixture directories under %s — run `make contract-capture GARM_REF=<sha>`", root)
	}
	sort.Strings(dirs)
	return dirs
}

func readShape(t *testing.T, path string) shapeDoc {
	t.Helper()
	var doc shapeDoc
	if err := json.Unmarshal(readFile(t, path), &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	env := make(map[string]string)
	for _, line := range strings.Split(string(readFile(t, path)), "\n") {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed env line %q in %s", line, path)
		}
		env[k] = v
	}
	return env
}

func readExit(t *testing.T, path string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(string(readFile(t, path))))
	if err != nil {
		t.Fatalf("parse exit code %s: %v", path, err)
	}
	return n
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// writeReplayConfig writes a provider config whose ssh transport is the test
// stub and whose two hosts match the instance IDs the fixtures use.
func writeReplayConfig(t *testing.T) string {
	t.Helper()
	body := fmt.Sprintf(`ssh_user = "garm"
ssh_bin = %q
hostctl_bin = "/usr/local/bin/tart-hostctl"
image = "ghcr.io/cirruslabs/macos-tahoe-xcode@sha256:contract"
slots_per_host = 2
create_timeout_seconds = 30
command_timeout_seconds = 15
probe_timeout_seconds = 3

[[hosts]]
name = "mac-mini-01"
address = "10.20.0.11"
port = 22

[[hosts]]
name = "mac-mini-02"
address = "10.20.0.12"
port = 22
`, sshStubPath(t))
	path := filepath.Join(t.TempDir(), "provider.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// setupStub writes canned hostctl responses and points the ssh stub at them, so
// the provider's probe-then-create flow completes offline and deterministically.
func setupStub(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
		return p
	}
	status := write("status.json",
		`{"tart_version":"tart 2.0.0","keychain_unlocked":true,"free_disk_bytes":100000000000,"drained":false,"capacity":{"total":2,"used":0,"adopted":0,"quarantined":0},"instances":[]}`)
	instance := write("instance.json",
		`{"name":"garm-contract-000","provider_id":"mac-mini-01/garm-contract-000","state":"ready","ip":"192.168.64.9"}`)
	instances := write("instances.json", `[]`)

	t.Setenv("SSH_STUB_STATUS_OUTPUT", status)
	t.Setenv("SSH_STUB_CREATE_OUTPUT", instance)
	t.Setenv("SSH_STUB_GET_OUTPUT", instance)
	t.Setenv("SSH_STUB_LIST_OUTPUT", instances)
	t.Setenv("SSH_STUB_EXIT", "0")
}

// sshStubPath resolves the testdata/bin/ssh stub used to replay hostctl output.
func sshStubPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(contractDir(t), "..", "..", "testdata", "bin", "ssh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("ssh stub missing: %v", err)
	}
	return abs
}

// contractDir returns the directory holding this test file.
func contractDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

// ---- JSON shape helpers ---------------------------------------------------

// keysOfJSON returns the sorted dotted-path key set of a JSON document.
func keysOfJSON(t *testing.T, raw []byte) []string {
	t.Helper()
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	seen := map[string]bool{}
	collectKeys(v, "", seen)
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func sortedOps(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionOps(a, b shapeDoc) map[string]bool {
	m := make(map[string]bool)
	for k := range a.Operations {
		m[k] = true
	}
	for k := range b.Operations {
		m[k] = true
	}
	return m
}
