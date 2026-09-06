# Releases

The current version is **v0.4.0**, following v0.3.0 of 2026-09-06. Source builds
report the source default; release packaging stamps the tag version with
`-X main.version`. Update the source default and changelog for each release.

Three things install from the published archives and so depend on their names
staying `orkestar-<os>-<arch>.<tar.gz|zip>` beside a `SHA256SUMS`: `install.sh`,
`install.ps1`, and mise through its `ubi` backend, which picks an asset by
matching the platform in that name. Renaming an archive breaks all three
silently, since each simply fails to find its file.

Before publishing, run the full macOS/Linux CI and native Windows job. Windows
checks include ConPTY final output, bounded writes, PowerShell session reattachment
and reset over named pipes. Installed agent model-turn/hook validation remains in
[validation.md](validation.md); fixtures do not establish that coverage.

Commit and push the reviewed project changes, then tag that commit (`v0.4.0` for
the current release) and
publish a GitHub release. `.github/workflows/release.yml` checks out that exact tag,
tests it, builds six archives, and uploads them with SHA256SUMS. The workflow can
be rerun with an existing release tag. A tag push alone does not publish a release
or trigger packaging.

The formula follows [thisyou's formula](https://github.com/martintrifunov/homebrew-tap/blob/main/Formula/thisyou.rb)
and builds from the tagged source. The tap owns its own updates: `update-orkestar.yml`
in `martintrifunov/homebrew-tap` reads this repository's latest release, rewrites the
formula's `url` and `sha256`, then runs `brew audit --strict`, a source build and
`brew test` before committing. It runs on a schedule and on manual dispatch, so no
cross-repository token is needed here. The source checksum can only be computed
after the tag exists remotely.

That workflow exits early when the formula already targets the released version,
**before** it audits, builds or tests. So it validates a version bump, not a
formula written by hand. After seeding or editing the formula directly, verify it
yourself instead:

```sh
brew install martintrifunov/tap/orkestar
brew test martintrifunov/tap/orkestar
brew audit --strict martintrifunov/tap/orkestar
```

Once the release and formula are published:

```sh
brew install martintrifunov/tap/orkestar
```

For Windows, users install with
`irm https://raw.githubusercontent.com/martintrifunov/orkestar/main/install.ps1 | iex`,
matching thisyou. The script is served from `main`, not from a tag, so it always
resolves `releases/latest/download`; keep it working for the newest release. It
selects amd64/arm64, verifies SHA-256, and installs without admin rights. `build.ps1 -Task Build|Test|Install|Uninstall` supports source development.
Uninstall removes the executable and PATH entry, retaining user data. Upgrades
must wait until a running daemon can be deliberately stopped, since Windows may
lock its executable.

Local packaging (no publishing):

```sh
python3 scripts/package-release.py v0.1.0
# Only after the tag exists on GitHub:
python3 scripts/package-release.py v0.1.0 --formula-only
```
