// Command shim is the "external provider" GARM execs during a contract
// capture. GARM runs it with the exact environment and stdin it would send the
// real provider, and parses its stdout. The shim records that environment and
// stdin to files, then prints a GARM-valid canned response so the capture
// round-trips without real infrastructure.
//
// The output directory is baked in at build time via
//
//	go build -ldflags "-X main.outDir=/abs/path" ./shim
//
// because GARM execs the provider binary with no arguments and a minimal
// environment (no PATH), so there is no other channel to pass a location in.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// outDir is set at build time (see the package comment). When empty — the
// shim is run directly, outside a capture — everything is written under the
// system temp directory.
var outDir string

// canonicalInstance is the canned ProviderInstance the shim returns for
// CreateInstance and GetInstance. It is deliberately the full field set so the
// capture exercises every key GARM's ProviderInstance can carry. The values
// are stable placeholders; the contract cares about the shape, not the values.
const canonicalInstance = `{"provider_id":"mac-mini-01/garm-contract-000","name":"garm-contract-000","os_type":"linux","os_name":"macos","os_version":"26","os_arch":"arm64","addresses":[{"address":"192.168.64.9","type":"private"}],"status":"running"}`

func main() {
	dir := outDir
	if dir == "" {
		dir = os.TempDir()
	}
	op := os.Getenv("GARM_COMMAND")
	if op == "" {
		op = "unknown"
	}

	// Record the exact environment GARM supplied. os.Environ() is precisely the
	// child environment, because garm-provider-common util/exec sets cmd.Env to
	// the list GARM built (nothing is inherited from our own process).
	env := strings.Join(os.Environ(), "\n")
	mustWrite(filepath.Join(dir, op+".env"), []byte(env))

	// Record stdin verbatim. CreateInstance carries a BootstrapInstance JSON;
	// every other operation has empty stdin.
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shim: read stdin: %v\n", err)
		os.Exit(1)
	}
	mustWrite(filepath.Join(dir, op+".stdin"), stdin)

	// Emit the canned response and record it as the expected stdout.
	var out string
	switch op {
	case "CreateInstance", "GetInstance":
		out = canonicalInstance + "\n"
	case "ListInstances":
		out = "[]\n"
	default:
		// DeleteInstance, RemoveAllInstances, StartInstance, StopInstance:
		// GARM expects no stdout.
		out = ""
	}
	mustWrite(filepath.Join(dir, op+".stdout"), []byte(out))
	fmt.Print(out)
}

func mustWrite(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "shim: write %s: %v\n", path, err)
		os.Exit(1)
	}
}
