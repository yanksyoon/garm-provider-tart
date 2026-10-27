# garm-provider-tart

A [GARM](https://github.com/cloudbase/garm) external provider that manages ephemeral macOS VMs on
Apple Silicon Mac minis with [Tart](https://tart.run).

Each GARM runner maps to one Tart VM: clone from a golden image, boot headless, wait for the guest,
run the runner bootstrap, and delete the VM when the job is done. The provider speaks the standard
GARM external-provider CLI contract (`CreateInstance`, `DeleteInstance`, `GetInstance`, `ListInstances`,
`RemoveAllInstances`, `Start`, `Stop`), so GARM treats it like any other provider.

## Status

Design and scaffolding in progress. Nothing here is deployable yet.

## Why a separate provider

GARM has no macOS support: its OS types are `linux` and `windows`, its bootstrap assumes systemd, and
it ships no darwin build. The provider therefore

- runs on the Linux GARM controller and drives Tart on each Mac mini over SSH,
- selects the `osx`/`arm64` runner tool from the bootstrap payload itself,
- runs the runner install script that GARM injects into `extra_specs["runner_install_template"]`
  inside the guest (launchd, JIT credentials, no `systemd`),
- caps each host at two macOS guests, which is the limit in the macOS licence agreement.

## Planned layout

```text
cmd/garm-provider-tart/   CLI entry point (GARM command dispatch)
internal/                 config, host driver, slots, state, scheduler, template rendering
scripts/mac/              host bootstrap, image refresh, runner install template
docs/                     ADR, reference, how-to
```

## Requirements

- GARM on Linux (no darwin builds are published).
- Apple Silicon Mac minis running macOS 26, reachable over SSH.
- Tart, a golden image in an OCI registry, and an unlocked `login.keychain` on each host.

## Licence

TBD.

## Development

```bash
go build ./...
go vet ./...
go test -race ./...
shellcheck -S warning $(find scripts -name '*.sh' 2>/dev/null)
npx --yes markdownlint-cli2 $(find . -name '*.md' -not -path './.git/*')
```

CI runs the same commands (`.github/workflows/ci.yml`, job `ci`). `main` is protected: every change lands
through a pull request with a green `ci` check.

