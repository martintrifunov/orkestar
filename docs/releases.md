# Releases

The first version is **v0.1.0**. Source builds report 0.1.0; release packaging stamps
the tag version with `-X main.version`. Update the source default and changelog
for subsequent releases.

Before publishing, run the full macOS/Linux CI and native Windows job. Windows
checks include ConPTY final output, bounded writes, PowerShell session reattachment
and reset over named pipes. Installed agent model-turn/hook validation remains in
[validation.md](validation.md); fixtures do not establish that coverage.

Commit and push the reviewed project changes, then tag that commit `v0.1.0` and
publish a GitHub release. `.github/workflows/release.yml` checks out that exact tag,
tests it, builds six archives, and uploads them with SHA256SUMS and `orkestar.rb`.
The workflow can be rerun with an existing release tag. A tag push alone does not
publish a release or trigger packaging.

The formula follows [thisyou's formula](https://github.com/martintrifunov/homebrew-tap/blob/main/Formula/thisyou.rb)
and builds from the tagged source. To update the tap automatically, set repository
secret `HOMEBREW_TAP_TOKEN` to a token with Contents read/write access to
`martintrifunov/homebrew-tap`. Without it, download `orkestar.rb` from the release,
review it, and commit it as `Formula/orkestar.rb` in that repository. The source
checksum can only be generated after the tag exists remotely.

Once the release and formula are published:

```sh
brew install martintrifunov/tap/orkestar
```

For Windows, download `install.ps1` from the same release tag and run it in
PowerShell. It selects amd64/arm64, verifies SHA-256, and installs without admin
rights. `build.ps1 -Task Build|Test|Install|Uninstall` supports source development.
Uninstall removes the executable and PATH entry, retaining user data. Upgrades
must wait until a running daemon can be deliberately stopped, since Windows may
lock its executable.

Local packaging (no publishing):

```sh
python3 scripts/package-release.py v0.1.0
# Only after the tag exists on GitHub:
python3 scripts/package-release.py v0.1.0 --formula-only
```
