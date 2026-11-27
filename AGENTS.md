# Agent Instructions

**Read [`CLAUDE.md`](./CLAUDE.md) for build/test commands, architecture, and the
per-plugin design notes — it is the source of truth for this repo. The rules
below cover the workflow around it.**

@CLAUDE.md

## Branch and commits

- Feature work happens on `rate-limiting` (fork `diarworld/gpustack-higress-plugin`);
  `main` tracks upstream. Branch from and push to `rate-limiting`.
- Commit messages follow Conventional Commits with the plugin as the scope:
  `feat(gpustack-rate-limit): ...`, `fix(gpustack-token-usage): ...`. Subject is
  lowercase and imperative. No `Co-Authored-By` or other agent-attribution
  trailers. Never use a GitHub closing keyword in front of an issue reference —
  use `ref`.

## Before pushing a plugin change

- `make -C extensions test-all` (go test per plugin) and `make lint` (ruff) must
  pass. Unit tests run without a proxy-wasm host: keep new logic in pure
  functions and pin behavior changes with tests next to the code, as the
  existing `*_test.go` files do.
- Bump `extensions/<name>/VERSION` for every behavioral change. The server
  derives the wasm URL version from the wheel's `manifest.json`, so shipping new
  bytes under an old VERSION means the gateway never runs them.
- The package version in `pyproject.toml` is released by tag: stamp it with a
  `chore(release): stamp package version X.Y.Z` commit and tag `vX.Y.Z` —
  `release.yaml` builds the wheel and publishes to PyPI + GitHub Release.

## How a change reaches prod

Tag `vX.Y.Z` → `release.yaml` builds the wheel and publishes it to PyPI and a
GitHub Release. How the wheel reaches the gateway image (plugin image build, the
rl-release chain, version markers) is driven by the private ops workspace outside
this repository — see the workspace-level `AGENTS.md` one directory up. Releasing
after a verified change is the default; no confirmation is needed.
