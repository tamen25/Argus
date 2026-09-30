# Releasing

Pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`. It builds everything
and attaches it to a **draft** GitHub release. Nothing is public until a
maintainer publishes the draft.

## What a release contains

| Asset | Built by | Contents |
|---|---|---|
| `argus_<version>_<os>_<arch>.tar.gz` / `.zip` | goreleaser (`.goreleaser.yaml`) | the `argus` CLI for linux, darwin and windows on amd64 and arm64, plus `LICENSE` and `README.md` |
| `tamen25-argus-app-<version>.zip` | npm + mage | the Grafana app plugin: frontend bundle and backend binaries for every platform Grafana runs on |
| `checksums.txt` | goreleaser | SHA-256 of every archive **and** the plugin zip |

Every binary is built with the Go toolchain pinned by `engine/go.mod`'s
`toolchain` line, the same one CI uses. A released binary's standard library
therefore depends on the repository, not on whoever cut the release. `argus version`
prints the tag, and the binary embeds the Instrumentation Score spec commit from
`.instrumentation-score-version`, so `argus score` reports the spec version even
without the repository on disk.

## Cutting a release

1. `main` is green, the phase gate is met, and `DECISIONS.md` records every cut.
2. Tag and push:

   ```bash
   git tag -a v1.0.0 -m "v1.0.0"
   git push origin v1.0.0
   ```

   Tags with a suffix (`v1.0.0-rc.1`) are marked as pre-releases.
3. When the workflow finishes, open the draft release on GitHub and paste in the
   release notes. They are written by hand: they carry the positioning (an
   independent implementation of the Instrumentation Score spec) and the
   fidelity caveats, which a generated changelog would not.
4. Publish the draft.

## Plugin signing

Grafana loads an unsigned plugin only when it is explicitly allowed
(`allow_loading_unsigned_plugins`), and the plugin catalog requires a signature.
If the repository secret `GRAFANA_ACCESS_POLICY_TOKEN` is set (a Grafana Cloud
access policy token with the `plugins:write` scope), the workflow signs the
plugin before zipping it. If it is not set, the zip is unsigned and the workflow
says so with a warning.

## Checking the configuration before a tag

CI runs `goreleaser check` on every pull request, because the release workflow
itself only runs on a tag. To build everything locally without publishing:

```bash
# Engine archives, into ./dist (git-ignored). PLUGIN_RELEASE_DIR is where the
# workflow stages the plugin zip; an empty directory is fine for a dry run.
mkdir -p /tmp/plugin-release
docker run --rm -v "$PWD:/src" -v /tmp/plugin-release:/plugin -w /src \
  -e PLUGIN_RELEASE_DIR=/plugin -e ARGUS_SPEC_VERSION="$(cat .instrumentation-score-version)" \
  goreleaser/goreleaser:v2.18.2 release --snapshot --clean --skip=publish

# Plugin backend for every platform, into plugin/dist
go install github.com/magefile/mage@latest
cd plugin && npm ci && npm run build && mage -v
```
