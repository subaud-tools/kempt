# Changelog

## 0.11.0

- kempt moves to tools-common v0.30.0, where a tool's `update` asks a gated site's signed links for its `Config.Product`. kempt's is its own name, so nothing changes in use: `kempt update` still downloads anonymously from kempt.tools.

## 0.10.0

- A `[[packages.x.download]]` step takes an optional `product`: what a gated site's signed links are asked for, when one product covers several tools (tackle's six say `product = "tackle"`). Without it the product is the step's `tool`, as before. `apply`, `outdated`, `upgrade` and `update`'s roll all ask for it; `kempt schema` and the spec list it.

## 0.9.1

- A sign-in whose code expired before it was allowed is replaced in the same run, so the run always shows a code to type (tools-common v0.29.0).
- With no sign-in, a gated download says to run `kempt login` once the machine is allowed, then the same command again. It said `kempt apply`, which downloads nothing when the tool is already installed, so it never reached the sign-in.

## 0.9.0

- **Gated downloads.** hail.tools and diazo.tools are closing `/dl` to anyone. A `[[packages.x.download]]` step on either site now fetches `latest`, the tarball and its `.sha256` through five-minute signed links the subaud.tools access service gives this machine's token, with the step's tool as the product; `outdated` and `upgrade` read `latest` the same way. The sha256 check holds as before. Every other site is fetched plainly, as before. With no token, or one the service refuses, `apply` stops at that step and says to run `kempt login`, then apply again; a tool the account doesn't own names the account page.
- `kempt login` and `kempt logout` (from tools-common 0.28.0): `login` signs the machine in to its subaud.tools account over two short runs, the first printing a code to type on the account page, the next saving the token once the machine is allowed; `--wait` does both in one run. The sign-in is shared by every subaud tool on the machine.
- Built on tools-common v0.28.0 (was v0.12.0).
- A failed download names its URL without the query, so a signed link's signature never reaches the output.

## 0.8.0

- Module path is now github.com/subaud-tools/kempt; the source lives at github.com/subaud-tools/kempt.

## 0.6.1

- `git-clone` accepts an existing checkout whose origin is the same repository over SSH (as `git@host:owner/repo`, or an `ssh://` URL as user `git` or none) or HTTPS. Only those plain forms are normalised: a remote with a non-default port, another SSH user, an absolute scp path, a percent-escape, query or fragment, a bare `host:path`, or a local path must match exactly. Before, one cloned over SSH under an HTTPS URL, or the reverse, showed as blocked on every run and stopped the rest of its package. `layer add` uses the same comparison for a checkout that already exists. (#41)

## 0.6.0

- **Layers.** A machine can apply manifests on top of its base: `kempt layer add <name> <git-url | path>` for a work, personal or machine-only layer, and `kempt layer add -project <dir>` for a project's `.kempt/kempt.toml`. A layer file says `[kempt] layer = "user"` or `"project"`. `plan`, `apply`, `update`, `doctor`, `verify`, `outdated`, `upgrade` and `refresh` converge the base and every layer as one plan, and `update` pulls git layers (never a project checkout). `layer list`, `layer remove` and `layer apply` manage them; `adopt`/`drop -layer` edit a layer's packages.
- Layers compose instead of fighting. Merges from different layers into one file fold into one step, so a layer's entry in an array the base sets with `arrays = "replace"` survives every converge (a private overlay's settings were undone by each `kempt update` before). A later layer's pin of a pi/npm entry wins and the plan shows `^ <entry> (<layer> overrides base: ...)`. Two layers claiming one symlink, clone, binary or service label is a plan error naming both.
- A project layer may only write files inside its checkout, and one added with `-project` is held when its file changes, until `kempt layer apply <name>`: a teammate's commit cannot change your machine unseen.
- A layer whose source is missing is reported `skipped layer <name>: ...` and the rest converges.

## 0.5.10

- `update` restarts into the new binary once it has replaced itself, so the roll and converge run the code it just installed. Before, they ran in the replaced process, so a fix to either only took effect on the next `update` (0.5.8 -> 0.5.9 printed 0.5.8's false "rolled" lines). If the restart fails, `update` says so and carries on with the old binary, as it did before.

## 0.5.9

- `update` and `upgrade` now actually roll unversioned `npm:` pi entries. They ran `pi install <entry>`, which keeps the caret range pi recorded at first install, so a 0.x minor or a new major never landed (pi-hail stayed on 0.1.1 behind 0.7.0) while kempt printed "rolled". They now run `pi update <entry>`, which installs the latest release and leaves the settings entry unversioned.
- A roll is confirmed by re-reading the installed version. One that leaves the entry behind is reported as `skipping <pkg>: still at X after the roll (latest Y)` by `update` and fails `upgrade`, instead of being counted as rolled.
- `update` holds a major-version bump instead of rolling it: `held <name> A -> B (major release): run kempt upgrade <name>`, and the roll summary counts it (`… skipped, N held`). `upgrade` still takes it, marked `(major release)` in the list it asks about, and `outdated` marks it too. A 0.x minor is not a major bump. pi-mcp-adapter 3.0 stopped reading the config file 2.x used, so a silent major roll can break a working setup.

## 0.5.8

- Checked by the stricter family lint set (tools-actions v0.5.0); the few findings are fixed or carry a stated reason. No behaviour change.
- `just verify` and the pre-push hook now run exactly what CI runs, at the version CI pins.
- The stale `install.sh` copy is removed from the repo; `https://kempt.tools/install.sh` is the installer.

## 0.5.7

- macOS binaries are signed with the Developer ID and notarized, so a copy downloaded in a browser runs instead of being quarantined by Gatekeeper. Releases are built through the family release actions (`schuettc/tools-actions`); the assets and `/dl` paths are unchanged.

## 0.5.6

- Built on tools-common v0.8.2: `kempt help <command>` and `<command> -h` show the command's summary line, and `kempt man` escapes quotes and leading dots correctly.
- Dependabot proposes each tools-common and tools-actions release as a PR.

## 0.5.5

- `update` always reports where the binary stands (`kempt X (current)` or `kempt updated A -> B`) and ends the roll step with `rolling: N checked, M rolled, K skipped`, so a run with nothing to do is visibly a no-op

## 0.5.4

- unpinned `git:` pi entries now roll: `update`/`upgrade` move them to the repo's default-branch head and `outdated` reports them (installed and latest are shown as short commit shas)

## 0.5.3

- service steps take `start-interval` (launchd `StartInterval`) for periodic jobs
