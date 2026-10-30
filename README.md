# kempt

kempt is declarative machine setup you can read before you run: a single manifest describes the software and files a machine should have, and kempt shows you exactly what it would change before it changes anything. The manifest is `kempt.toml`; its schema and primitives are specified in [docs/spec.md](docs/spec.md).

## Getting started

```sh
curl -fsSL https://kempt.tools/install.sh | sh
```

The installer downloads the latest release for your platform, verifies its
checksum, and drops the `kempt` binary in `~/.local/bin` (override with
`KEMPT_INSTALL_DIR`). To build from source instead:

```sh
go install ./cmd/kempt
```

Then point kempt at your config repo:

```sh
kempt init <repo-url>
```

`kempt init` fetches the manifest repo (default `~/.config/kempt/repo`), walks an
interactive picker to choose a profile and refine the exact set of packages,
saves your selection, and applies it to converge the machine. Pass `-profile
<name> -yes` to init non-interactively, or `-dir` to clone somewhere other than
the default.

The source can be a **git repo** (cloned; `kempt update` pulls it) or a
**gzipped tarball URL** ending in `.tar.gz`/`.tgz` — e.g. a GitHub archive
`https://github.com/<you>/<repo>/archive/refs/heads/main.tar.gz`. A tarball is
fetched and extracted (no git required; the wrapper directory is stripped), and
`kempt update` re-fetches and re-extracts it. Sharing a full config tree does
not require a cloneable git repo.

For a **self-contained** manifest (software installs + config merges + downloads,
with no repo-relative `symlink`/`git-clone` file sources), you can skip the repo
entirely and point `plan`/`apply`/`verify`/`lint` straight at a single file over
HTTP or stdin:

```sh
kempt plan -manifest https://example.com/kempt.toml
curl -fsSL https://example.com/kempt.toml | kempt apply -manifest - -yes
```

Once a selection is saved, the day-to-day commands operate on it without
repeating `-manifest`/`-profile`:

- **`kempt status`** — show the cached result of the last refresh.
- **`kempt refresh`** — fetch the repo and recompute pending changes; with
  auto-apply enabled it applies files-class changes only (never software).
- **`kempt update`** - pull the repo, self-update the binary, roll rolling
  entries (unversioned `npm`/`pi` and `download version = "latest"`) to latest,
  then converge (pinned entries to their pin). Opted-in downloads instead
  show their binary and matching setup together before applying either. A
  major-version bump is held,
  not rolled: `update` names it, and `kempt upgrade <name>` takes it.
- **`kempt outdated`** - list installed tools and extensions with newer
  releases (offline for pinned versions; resolves latest over the network for
  rolling entries).
- **`kempt upgrade [name…]`** - roll installed tools and extensions that are
  behind to latest, major releases included (marked as such in the list it
  asks about). Prompts unless `-yes`. Opted-in release setup is resolved online
  before review in `init`, `apply`,
  `update` and `upgrade`; `plan` reads its cached inputs offline. Binary-only
  downloads retain their existing presence/pin inspection behavior.
- **`kempt layer add <name> <git-url | path>`** / **`kempt layer add -project <dir>`**
  - apply a layer on top of the base on this machine: a work or personal
  manifest, one that never leaves the machine, or a project's own
  `.kempt/kempt.toml`. `update` then converges base and layers as one plan.
  `layer list`, `layer remove <name>` (uninstalls nothing), and `layer apply <name>`
  (re-applies, and accepts a changed project layer, which `update` otherwise
  holds). `adopt`/`drop -layer <name>` edit a layer's packages.
- **`kempt doctor`** — diagnose machine-vs-manifest drift (read-only). Reports
  drift the `plan`/`verify` commands miss: undeclared live array entries,
  duplicate package identities, orphaned installs, broken/foreign managed
  symlinks, missing/drifted release-managed files attributed to their packages,
  and a `plan`/`verify` rollup. Exits non-zero when findings reach the
  failing threshold (`error` by default; `warn` under `-strict`). `-json` emits
  machine-readable findings.
- **`kempt adopt <pkg>`** / **`kempt drop <pkg>`** — add or remove a package
  (and its needs) in the saved selection.
- **`kempt config auto-apply-files [true|false]`** — get or set whether refresh
  auto-applies files-class changes.

## Release-owned setup

A participating release can supply its matching wrapper, multi-file skills,
merges and other declarative integration. In a `spec = 2` manifest add
`setup = true` to that tool's `[[packages.<name>.download]]` block. See the
[full opt-in and publisher contract](docs/spec.md#release-owned-setup-spec-2).
Only opt in once the producer publishes the checksummed bundle; this engine
release does not mean all tools have migrated.

`apply` resolves and verifies binary plus setup before showing the plan; `plan`
uses the cached declaration and reports unresolved setup instead of claiming a
complete plan. Apply uses that invocation's frozen inputs, even if `latest`
changes while you review. A later apply resolves again. Repeat unchanged setup
converges without rewriting ownership records. Foreign files and local edits
are preserved as visible conflicts, not overwritten. Partial failures remain
incomplete and are repaired through the ordinary plan/apply path. No executable
installer is invoked to discover setup.

Spec 1 manifests remain supported. Files-only refresh will not apply candidate
setup ahead of its matching binary. Hook/plugin-specific reconciliation and
migration of the family's declarations are separate work, not included here.

## Commands

kempt has the following working commands:

- **`kempt lint`** — validate a manifest without touching the machine.
  ```sh
  kempt lint kempt.toml
  ```
- **`kempt plan`** — inspect the machine and print the deltas apply would make.
  ```sh
  kempt plan -manifest kempt.toml
  ```
- **`kempt apply`** — converge the machine to the manifest (prompts unless `-yes`).
  ```sh
  kempt apply -manifest kempt.toml -yes
  ```
- **`kempt verify`** — run the manifest's read-only verify checks.
  ```sh
  kempt verify -manifest kempt.toml
  ```
- **`kempt version`** — print the kempt version.
  ```sh
  kempt version
  ```
- **`kempt schema`** — print the JSON Schema for `kempt.toml` (point your
  editor at it for completion and validation).
  ```sh
  kempt schema > kempt.schema.json
  ```
- **`kempt new`** — scaffold a new kempt config repo in the given directory
  (defaults to the current one; refuses to overwrite an existing `kempt.toml`).
  ```sh
  kempt new my-config
  ```
- **`kempt dump`** — inspect the current machine and suggest a manifest
  (read-only; never touches anything). Pass `-repo` to detect dotfile symlinks.
  ```sh
  kempt dump > kempt.toml
  ```

`plan`, `apply`, and `verify` accept `-profile` and `-packages` to narrow the
selection. `plan` also accepts `-os` and `-arch` to dry-plan for another
platform (e.g. `kempt plan -manifest kempt.toml -os linux -arch arm64`) without
inspecting the local machine's real OS/arch.

## Coming next

Dotfiles-as-reference-config and the [kempt.tools](https://kempt.tools) site
land next.
