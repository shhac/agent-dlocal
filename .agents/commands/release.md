---
description: Release via tag push — CI builds, publishes, and bumps the Homebrew formula
argument-hint: <patch|minor|major>
---

# Release

Releasing `agent-dlocal` is automated. Pushing a `v*` tag triggers
`.github/workflows/release.yml`, which calls the shared `go-release` workflow in
`shhac/homebrew-tap` to cross-build every platform, publish the GitHub Release,
and regenerate + push `Formula/agent-dlocal.rb` (with shell completions) to the tap.
The tag also triggers `.github/workflows/publish-skill.yml`, which publishes
`skills/agent-dlocal` to `shhac/agent-skills`. **No manual build, and no manual
formula bump.**

## Steps

1. `$ARGUMENTS` must be `patch`, `minor`, or `major` — else stop and ask.
2. Pre-flight (CI re-runs tests on the tag, but check locally first):
   - Clean tree (`git status --short`), on `main`, up to date with `origin/main`.
   - Tests, vet, and lint pass (e.g. `make test` / `go test ./...`, `go vet ./...`,
     `make lint` / `golangci-lint run ./...`). The version is injected from the tag
     (`-ldflags -X main.version=…`) — there is no version file to edit.
   - `release.yml`'s `help_match` still appears in `agent-dlocal --help`. The
     formula test asserts it, so a reworded `Short` string fails the release
     rather than the build.
3. Compute the new version by bumping the latest tag
   (`git describe --tags --abbrev=0`): patch → x.y.(z+1), minor → x.(y+1).0,
   major → (x+1).0.0.
4. Tag and push — this is the whole release:
   ```bash
   git tag "v${new_version}"
   git push origin "v${new_version}"
   ```
5. Verify CI and the outputs:
   ```bash
   gh run watch --repo shhac/agent-dlocal          # release + Publish skill runs green
   gh release view "v${new_version}" --repo shhac/agent-dlocal   # 6 assets
   ```
   Install / upgrade: `brew install shhac/tap/agent-dlocal` · `brew upgrade shhac/tap/agent-dlocal`

## Manual fallback (only if the workflow itself is broken)

Re-run a failed release with `gh run rerun <id> --repo shhac/agent-dlocal`. To bypass
the workflow entirely, build the `GOOS/GOARCH` binaries with
`-ldflags "-s -w -X main.version=<v>"`, `gh release create` the tarballs, and edit
`Formula/agent-dlocal.rb` by hand.

## Secrets

The formula push authenticates via the `TAP_DEPLOY_KEY` secret in this repo's
`homebrew-tap` GitHub environment, paired with the read-write
"agent-dlocal release automation (env-scoped)" deploy key on `shhac/homebrew-tap`.
The skill publish uses the repo-level `SKILLS_DEPLOY_KEY`, paired with the
"agent-dlocal skill sync" deploy key on `shhac/agent-skills`. If the workflow logs
"TAP_DEPLOY_KEY not set — skipping tap update", rotate with a repo-specific
pair — pipe the private key, never echo it:

```bash
ssh-keygen -t ed25519 -N "" -C "agent-dlocal release automation" -f tap_key
gh repo deploy-key add tap_key.pub -R shhac/homebrew-tap --allow-write \
  --title "agent-dlocal release automation (env-scoped)"
gh secret set TAP_DEPLOY_KEY --repo shhac/agent-dlocal --env homebrew-tap < tap_key
rm tap_key tap_key.pub
```

The `homebrew-tap` environment restricts deployments to `v*` tag refs, so a
branch build can never reach the tap write key.
