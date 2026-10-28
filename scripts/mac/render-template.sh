#!/usr/bin/env bash
# Render the macOS runner-install template with sample params, in both the JIT
# and non-JIT variants. The JIT variant is the default output on stdout; both
# variants are rejected if they leak Linux-only mechanisms and are checked for
# shell syntax with `bash -n`.
#
# Usage:
#   scripts/mac/render-template.sh            # print the JIT variant
#   scripts/mac/render-template.sh --nojit    # print the non-JIT variant
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workdir="$(mktemp -d "$repo_root/.render-template.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT

# A throwaway Go driver that renders both variants through the render helper
# (the same code path the unit tests and GARM use).
cat > "$workdir/main.go" <<'GOEOF'
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yanksyoon/garm-provider-tart/internal/macprovider"
)

func main() {
	outDir := os.Args[1]
	for _, jit := range []bool{true, false} {
		script, err := macprovider.InstallRunnerScript(macprovider.ExampleParams(jit))
		if err != nil {
			fmt.Fprintf(os.Stderr, "render %v: %v\n", jit, err)
			os.Exit(1)
		}
		name := "nojit.sh"
		if jit {
			name = "jit.sh"
		}
		if err := os.WriteFile(filepath.Join(outDir, name), script, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", name, err)
			os.Exit(1)
		}
	}
}
GOEOF

go run "$workdir/main.go" "$workdir"

# Reject Linux-only mechanisms that must never appear in a macOS install script.
for f in "$workdir/jit.sh" "$workdir/nojit.sh"; do
  if grep -Eq 'systemctl|installdependencies\.sh|apt-get' "$f"; then
    echo "render-template: $f contains a Linux-only mechanism" >&2
    exit 1
  fi
  bash -n "$f"
done

if [ "${1:-}" = "--nojit" ]; then
  cat "$workdir/nojit.sh"
else
  cat "$workdir/jit.sh"
fi
