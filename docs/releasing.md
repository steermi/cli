# Releasing

Maintainers only. Nothing here runs automatically from a pull request.

## Before tagging

1. `agent-contracts`: if this release needs a contracts change, release that
   module first and `go get github.com/steermi/agent-contracts@vX.Y.Z`.
2. Move **Unreleased** in `CHANGELOG.md` under the new version and date. Bump
   `plugin/steermi/.claude-plugin/plugin.json` `version` to match.
3. `make check vuln` and `python3 scripts/check-repo.py --release .` pass.
4. Rehearse: `make snapshot`, unpack the archive for your platform, run
   `./steermi version` and `./steermi doctor`.

## Tag and draft

```bash
git tag -s vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

`release.yml` verifies the tag is on `main`, re-runs every check, builds the
archives, checksums and SBOMs with GoReleaser into a **draft** release and
attests build provenance.

## Verify, then publish

On a clean machine (or container) for each runtime-verified platform:

```bash
gh release download vX.Y.Z --repo steermi/cli
sha256sum --check --ignore-missing checksums.txt
gh attestation verify steermi_X.Y.Z_Linux_x86_64.tar.gz --repo steermi/cli
tar -xzf steermi_X.Y.Z_Linux_x86_64.tar.gz steermi && ./steermi version
```

Also check `go install github.com/steermi/cli/cmd/steermi@vX.Y.Z` once the
tag is public. Then publish the draft. Releases are immutable: fix a bad
release with a new patch version, never by replacing assets.

## After publishing

Update the Steermi web onboarding command if it pins a version, and announce
the release in Discussions.
