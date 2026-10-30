# The kempt standard (`kempt.toml`)

A kempt config repo is one `kempt.toml` plus the config files it references.
The manifest is **fully declarative**: kempt executes only built-in, versioned
primitives. There is no exec/script escape hatch. Every action is reviewable
before it runs.

This document is the standard, versioned by the `spec` field.

## Shape

```toml
[kempt]
spec = 1

[packages.core]
description = "shell, prompt, history, modern CLI tools"

  [[packages.core.install]]
  brew   = { formulas = ["starship", "atuin", "ripgrep", "jq"] }
  winget = ["BurntSushi.ripgrep.MSVC", "jqlang.jq"]
  apt    = ["ripgrep", "jq"]

  [[packages.core.symlink]]
  from = "config/zsh"
  to = "~/.config/zsh"
  backup = true

[packages.terminal]
description = "Ghostty + tmux + yazi workspace"
needs = ["core"]

  [[packages.terminal.git-clone]]
  repo = "https://github.com/tmux-plugins/tmux-resurrect"
  to = "~/.tmux/plugins/tmux-resurrect"

[packages.muster]
description = "local multi-agent coordination bus"
only = { os = "darwin" }

  [[packages.muster.github-release]]
  repo = "subaud-tools/muster"
  asset = "muster_{os}_{arch}.tar.gz"
  bin = "muster"

  [[packages.muster.json-merge]]
  file = "~/.claude/settings.json"
  merge = { mcpServers.muster = { command = "muster", args = ["mcp"] } }

  [[packages.muster.service]]
  label = "tools.muster.serve"
  program = ["muster", "serve"]

  [[packages.muster.verify]]
  command-exists = "muster"
  version-current = { repo = "subaud-tools/muster", command = "muster version" }

[profiles.developer]
description = "full toolchain: terminal, editors, agents"
packages = ["core", "terminal", "muster"]

[profiles.minimal]
description = "just a pleasant shell"
packages = ["core"]
```

The `muster` package above is a minimal illustration. For the complete,
production package — MCP registration and session hooks for Claude, Codex, and
Cursor, the tmux mailbox, and the daemon service — run `muster setup --print
kempt` (muster emits this exact block, so it never drifts from what
`muster setup` applies for non-kempt users).

## Primitives (v1, closed set)

The primitive set is closed. When a real capability gap appears, the answer is
a new versioned primitive in kempt, not a script hook.

| Primitive | Safety class | Notes |
|---|---|---|
| `install` | software | Per-OS/cross-platform backends: `brew` (formulas/casks/taps), `winget`, `apt`, `npm` (global packages), `pi` (more later). Backends are additive — `npm`/`pi` install on any host that has the runtime, alongside the OS backend. Kempt selects the applicable backends at plan time; a platform with no matching backend skips or plan-errors per `only`/`needs`. `install.npm` and `install.pi` entries may be **pinned** as `name@version` (version embedded in the string — no schema change): kempt then converges to that exact version, reinstalling on `kempt update` when the installed version differs. Unversioned `npm`/`pi` entries are ROLLING: they track the newest published version (for a `git:` pi source, the head of the repo's default branch, compared by commit sha), rolled forward by `kempt upgrade` and by the `kempt update` roll step, and reported by `kempt outdated`; the offline `plan`/`apply` treat them as presence-only (install if absent, never rolled), so the plan stays deterministic. |
| `github-release` | software | Asset templating `{os}`/`{arch}`; downloads asset + `checksums.txt`, sha256-verifies, stages to a unique fsynced temp beside the target, atomic rename into `~/.local/bin` (running daemons keep their inode). |
| `managed-file` | files | Spec 2: writes one verified release-bundle file with explicit destination/mode and recorded ownership; foreign content blocks. See release-owned setup below. |
| `download` | software | Fetch a binary from a domain distribution (not GitHub). Resolves `version` (`latest` by default, or a pinned semver), downloads the family URL, verifies its `.sha256` sidecar, and installs `bin` into `~/.local/bin` via the same stage-and-atomic-`mv` path as `github-release`. See the family-URL contract below. |
| `git-clone` | software | Pinned ref or branch. |
| `service` | software | Backend: launchd (macOS), systemd `--user` (Linux); Windows later. Renders unit/plist, `cmp`-before-reload so unchanged services never restart. Fields: `label`, `program` (argv), plus optional `env`, `stdout`, `stderr`, `keep-alive`, `run-at-load`, `process-type`, `throttle-interval`, `start-interval` (seconds; a periodic job — requires `keep-alive = false`), `session-type` (mapped to the backend's native keys). |
| `symlink` | files | Repo-relative `from`; `backup = true` moves a real file to `.bak` first. Windows requires Developer Mode — detected and reported in plan, not failed mid-apply. |
| `json-merge` | files | Additive deep merge (jq semantics); idempotent; multiple packages may merge into the same file. `arrays` selects list behavior: `append` (default, union) or `replace` (desired list wins). Covers MCP registration and harness hooks. The literal token `${HOME}` in any merged string value is expanded to the machine's home at write time — for files that do not themselves expand `~` (e.g. codex `hooks.json`, whose command strings need absolute paths). Bare `~` is left untouched. `remove` lists dotted key paths (`"mcpServers.daybook.command"`) deleted before the merge, since TOML has no null to unset a key; a missing path is skipped, and a path `merge` sets again is replaced. |
| `toml-merge` | files | Additive deep merge for TOML config files (e.g. an agent's `config.toml`); idempotent; maps recurse, arrays append-missing, scalars overwrite. Expands the `${HOME}` token in string values like `json-merge`. |
| `line-in-file` | files | Ensure-line/ensure-block for shell includes and PATH nudges. |
| `verify` | read-only | Declared checks: `command-exists`, `command-exists-any` (any of a list on PATH), `http-ok` (URL returns 2xx), symlink target, `version-current` (GitHub latest-release drift). |

### `download` family-URL contract

The `download` primitive resolves assets from a predictable URL family so a
vendor need only publish files at these paths:

- **Artifact:** `https://<site>/dl/<tool>/<ver>/<tool>_<os>_<arch>.tar.gz`
- **Latest pointer:** a plain-text document holding a bare semver (e.g. `1.4.2`),
  used when `version` is `latest` or omitted, to resolve `<ver>`.
- **Checksum:** a shasum-format `.sha256` sidecar alongside the artifact
  (`<artifact>.sha256`), verified before install.

`<os>`/`<arch>` use kempt's normalized platform tokens (same as `github-release`
asset templating).

### `download` version field

The `version` field controls whether a download rolls automatically or locks to a
manifest-pinned version. When omitted, `version` defaults to `"latest"`.

**`version = "latest"`** (rolling): without `setup = true`, offline inspection
checks presence, not remote currency. An absent binary is an install change;
an installed one is a no-op. Apply fetches the latest version when installing
an absent binary, but does not roll an already present one. `outdated` checks
remote currency; `upgrade` and `update` roll installed tools. Update holds a
higher major version and names `kempt upgrade` as the explicit action; a 0.x
minor is not a major bump. Unversioned `npm`/`pi` entries also hold major bumps.
With `setup = true`, preparation resolves an exact candidate before review,
including the matching setup; see below.

**`version = "x.y.z"`** (pinned): The download locks to the specific semver. If
the installed version differs, `plan` shows `upgrade <bin> <old> -> <pin>` drift
offline, and `apply` converges to the pinned version (including a downgrade if
necessary). Version currency is managed separately via `kempt upgrade` and `kempt
outdated`.

### `download` product field

On a gated site (one whose `/dl` is closed to anyone; kempt knows which), each
file is fetched through a signed link the subaud.tools access service gives
this machine's token, asked for a product and a path. The product is the
step's `product`, or its `tool` when `product` is omitted. Set it when one
product covers several tools: tackle's six each say `product = "tackle"`.
`apply`, `outdated`, `upgrade` and the `kempt update` roll step all ask for it.
An ungated site ignores it.

### Release-owned setup (spec 2)

Opt in per download, only for a release that publishes a setup bundle:

```toml
[kempt]
spec = 2

[packages.proj]
[[packages.proj.download]]
site = "tackle.tools"
product = "tackle"
tool = "proj"
bin = "proj"
version = "latest"
setup = true
```

This is the consumer syntax, not a claim that every family release has migrated.
Omitting `setup` retains the ordinary binary-only download behavior.

**Resolution and review.** `init`, `apply`, `update` and explicit `upgrade`
prepare opted-in releases online before displaying their combined binary/setup
plan. Preparation verifies both assets and caches their bytes and checksums
under kempt's state directory. It does not write installation targets or run a
tool's installer. Rolling preparation holds major upgrades and does not downgrade;
pins select their exact release. `upgrade` can explicitly take a held major.
Application uses the frozen bytes shown in that invocation's plan, not a second
lookup of `latest`. A separate later `apply` prepares again; a printed `plan`
is not a saved approval artifact.

`plan`, `doctor`, `verify` and refresh's setup inspection use cached declarations,
with no release-client requests. Missing, invalid or mismatched cached setup
blocks the download and names `kempt apply` as the resolution action. Release-owned
remote verification checks also report a blocker offline instead of fetching.
This does not make every existing operation network-free: an explicitly remote
`-manifest` still needs fetching, refresh retains its repo fetch, and legacy
non-setup verification retains its existing behavior. Files-only refresh holds
candidate integration when its matching binary still requires a software change.

**Bundle format.** Publish
`https://<site>/dl/<tool>/<version>/<tool>_setup.tar.gz` and its `.sha256`
sidecar beside the binary archive. Both use the same exact version and the same
`product`/entitlement routing. The gzip tar contains only regular files:

- `setup.json`: `{"format":1,"tool":"proj","version":"1.2.3"}`. Bundle
  format 1 is independent of kempt manifest spec 2.
- `setup.toml`: a spec-2 fragment with exactly `[packages.integration]`, no
  profiles or layer declaration, and no `download`, `github-release` or
  `git-clone` steps. Normal supported primitives provide merges, verification,
  services and other integration work; there is no arbitrary exec primitive.
- `files/<relative-path>`: referenced assets, with no traversal or symlinks.

The fragment is namespaced as `<parent-package>/setup/<bin>`, depends on its
binary parent, and participates in normal composition/conflict handling.
Its repo-relative paths use the actual selected checkout, not `~/dotfiles`.
For bundle assets use explicit managed-file steps, one per file:

```toml
[kempt]
spec = 2
[packages.integration]
[[packages.integration.managed-file]]
from = "zsh/proj.zsh"
to = "~/.config/zsh/proj.zsh"
mode = "0644"
[[packages.integration.managed-file]]
from = "skills/proj/SKILL.md"
to = "~/.claude/skills/proj/SKILL.md"
mode = "0644"
[[packages.integration.managed-file]]
from = "skills/proj/reference.md"
to = "~/.claude/skills/proj/reference.md"
mode = "0644"
```

`from` is relative to bundle `files/`; `to` expands like other destinations;
`mode` is explicitly `0644` or `0755`. A standalone managed-file declaration
without a verified bundle source is blocked. A multi-file skill lists every
owned file; no directory-wide replacement or removal of unlisted files occurs.

**Ownership and failure.** Kempt records tool, destination and content digest in
`managed-files.json` in its state directory. Missing files are created;
unchanged owned content can be updated; identical content can be adopted.
A different recorded owner, locally modified recorded content, a symlink or
other non-regular destination, or foreign content blocks replacement. Existing
tool-installed `SKILL.md` files are recognized by the shared skills ownership
rule; auxiliary files still need identical content or kempt's own matching
record. Unchanged repeats do not rewrite contents or ownership records.

Failures are not transactional: earlier changes may remain. A failed binary
blocks dependent setup; folded setup merges also wait for their contributing
binary parents. Incomplete selected release setup fails the command, and the
next plan exposes remaining work. Resolve foreign-file conflicts explicitly;
kempt does not silently overwrite edits. Doctor attributes missing/drifted
managed-file findings to the namespaced owning package through its normal rollup,
with `kempt apply` for convergible changes and plan inspection for blockers.

**Producing bundles.** Generate from the same canonical sources used by the
tool's own setup/embedded skills, using tools-common `setupbundle.Build(tool,
version, manifestBytes, filesFS)` (available since v0.36.0). Write its bytes as
`dist/<tool>_setup.tar.gz` before the shared go-release action runs. List that
file explicitly in the action's existing `extra-assets` input:

```yaml
extra-assets: dist/proj_setup.tar.gz
```

Use tools-actions v0.13.0 or later for extra-asset publication to `/dl`, with
its normal `dl-tool`, bucket and release version inputs. The publisher creates
the shasum sidecar and publishes alongside the matching binaries; do not add a
second uploader or installer. For a product with several tools, publish each
bundle under its tool's versioned path. Older action pins that only attach
extras to GitHub are insufficient. Keep all family action pins aligned when
upgrading a producer. Kempt consumes bundles; this engine change does not add
bundle production to every tool's release workflow.

Safety class comes from kempt's primitive table — a manifest cannot declare its
own software installs "safe."

Every package may carry a `notes` field — an array of free-form strings surfaced
in plan output (post-install hints, manual follow-ups). Notes are documentation
only; they run nothing.

## Rules

- Steps run in written order within a package; packages run in dependency
  (`needs`) order. Planned for a later spec revision, not part of spec = 1: step-level
  `requires` preflights (e.g. "git exists") that fail the plan, not the apply. A spec = 1
  parser rejects `requires` as an unknown key.
- Every step is idempotent by contract: kempt computes current vs desired and
  no-ops on match. This is what makes plan honest and converge cheap.
- `only = { os, arch }` on packages or steps restricts where they apply; always
  visible in plan output.

## Profiles

Profiles are named package sets (personas: developer, minimal, …), **not**
conditionals. The first-run picker preseeds from the chosen profile. There is no
hostname/env conditional language in v1 — `only = { os, arch }` is the only
conditional.

## Layers

A machine can apply **layers** on top of its base manifest: work, personal,
machine-only, or a project's own configuration. Which layers a machine uses is
its saved state (`kempt layer add`), never a conditional in any manifest, and
the base manifest never names them. Full design:
`docs/superpowers/specs/2026-09-29-layers-design.md`.

- A layer is a `kempt.toml` whose `[kempt]` table sets `layer = "user"` (it
  configures the user's machine) or `layer = "project"` (it configures one
  checkout: file primitives only, relative targets inside the checkout).
- A layer's source is `git` (a checkout `update` pulls) or `path` (a
  directory on this machine that is only read). A project checkout is never
  pulled.
- Layers compose with the base into one plan, in order: base, user layers,
  project layers. A layer's packages are namespaced `<layer>/<pkg>`; relative
  paths resolve against the layer file's directory (a project layer: the
  checkout).
- Across layers, installs combine (a later layer's pin of an entry wins, and
  the plan says so); `json-merge` / `toml-merge` steps into one file fold into
  one step, so a `replace` array is the ordered union of what every layer
  declares (a layer's `append` to that array joins the union); and two layers claiming one symlink, clone, binary or service
  label is a plan error unless the definitions are identical.
- A layer added with `-project` is held, not applied, when its file changes,
  until `kempt layer apply <name>`.
- A layer whose source is missing is skipped with a message; the rest still
  converges.
- Layers apply to the saved selection only: a command given `-manifest`,
  `-profile` or `-packages` runs on exactly what it names.

## Versioning

The engine accepts `spec = 1` and `spec = 2`, rejecting all other versions.
Spec 1 retains the existing primitives; `setup = true` and `managed-file`
require spec 2 and are rejected in spec-1 declarations.
Future spec revisions bump this field, and older engines refuse manifests from
the future rather than mis-parse them. A published JSON Schema gives TOML LSPs
(taplo) completion and validation while authoring.

## doctor

`kempt doctor` is a read-only machine-vs-manifest drift and health check. It
reports divergence the other commands cannot see by design — the tolerant
semantics that keep `plan` honest also make some drift invisible to it — and it
never mutates the machine. Selection resolves identically to `plan`/`verify`
(the saved selection when flags are omitted); it accepts `-manifest`,
`-profile`, and `-packages`, plus `-json` (machine-readable findings) and
`-strict` (treat `warn`-level findings as failures for the exit code).

Each check yields zero or more **findings**, each carrying a `check` id, a
`severity` (`error`/`warn`/`info`), the attributable `package`, a `detail`, and
a concrete `remediation` string. `doctor` is report-only in v1 — there is no
`--fix`; the remediation is always an existing command (`kempt apply`,
`kempt adopt`, …).

| Check | What it reports | Severity |
|---|---|---|
| **D1** extra-array drift | Live elements of a `json-merge`-managed array the manifest does not declare (the inverse of the subset check `plan` uses, so it fires even for `arrays = "append"` merges). | `warn` (`error` when the step is `arrays = "replace"`) |
| **D2** duplicate identity | Entries in a managed array that normalize to the same identity — a trailing `@version` and the `npm:`/`git:` scheme are stripped, so `npm:pi-quiet@0.2.0` ≡ `npm:pi-quiet`; git identity is the repo URL without `@ref`, local is the resolved path. | `warn` |
| **D3** orphaned installs | Installed software absent from the manifest: `pi list` vs the union of `install.pi` specs, and (opt-in) `npm ls -g` vs `install.npm`. | `warn` (pi), `info` (npm) |
| **D4** broken/foreign managed symlinks | For every `symlink` step, `lstat` the `to` path and report broken (dangling), foreign (a real file/dir where the repo symlink belongs — the `backup`-would-fire case), or mispointed links, covering links with no `verify` step. | `error` (broken), `warn` (foreign/mispointed) |
| **D5** plan/verify rollup | Runs the plan and the `verify` steps for the selection and summarizes pending changes, blocked steps, and verify pass/fail, so ordinary "machine is behind the manifest" state shows in the same report. | `info` (managed-file changes, blocked steps and verify failures promoted to `error`, with package attribution) |

The **D3 orphan check is selection-scoped**: it compares the machine's full
installed set against the declared installs of the *selected* packages only. Run
`doctor` with the full saved selection (omit `-packages`) for an accurate orphan
report — a narrow `-packages` selection reports everything the other packages
install as an orphan, which is correct but noisy.

### Exit codes

- `0` — healthy: no findings at the failing threshold.
- `1` — findings present at the failing threshold (`error` by default; `-strict`
  lowers it to `warn`, so CI can gate hard; `info` never fails).
- `2` — usage/parse error, consistent with the other commands.

### `[doctor]` config

A top-level `[doctor]` block tunes the machine-health checks:

```toml
[doctor]
checkNpmOrphans = false
ignore = ["pi-quiet", "npm:some-tool", "glob:internal-*"]
```

- `checkNpmOrphans` (bool, default `false`) — enable D3's npm-orphan comparison.
  Off by default because a global npm install set is noisy with hand-installed
  tools.
- `ignore` (array of strings) — suppress known-intentional out-of-band installs
  from the orphan check. Each entry is either an exact spec, a backend-qualified
  id (`npm:<id>`), or a glob (`glob:<pattern>`).

### lint invariant (offline)

`kempt lint` enforces one manifest-internal rule that `doctor` does not: when a
package declares both an `install.pi` list and a `json-merge` that writes the pi
settings' `packages` array, the two lists must match **string-for-string**
(order-insensitive set equality, exact strings). This replaces the previous
comment-and-discipline convention so a mismatch fails offline at lint time before
it ever reaches a machine. The rule keys on "an `install.<backend>` list and a
`json-merge` writing that backend's registry array in the same package," so it
generalizes to a future npm equivalent.

## Implementation status

- `kempt lint` — shipped (unknown keys, spec validation, structural checks,
  install-list ↔ settings-merge `packages` string-for-string invariant).
- `kempt doctor` — shipped (read-only machine-vs-manifest drift/health check;
  D1–D5 findings, `[doctor]` config, `-json`, `-strict`).
- `kempt plan` / `apply` / `init` / `update` — in progress (phases 1b–1c).
