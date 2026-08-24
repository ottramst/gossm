# Contributing

## Pull requests

Pull requests are **squash-merged**: your PR title becomes the commit
message on `master`, so it must follow
[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <description>
```

Examples:

- `feat: add region flag to the fwd command`
- `fix(ssh): quote identity file paths with spaces`
- `docs: describe container usage`

A CI check validates the title; the standard types are accepted
(`feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore`, `revert`). Individual commits inside a PR can be
whatever you like — only the title matters.

Release notes are generated from these commit messages: `feat` lands
under Features, `fix` under Bug fixes, `chore(deps)` under
Dependencies; `docs`/`test`/`ci`/`style` are excluded.

## Development

```sh
make build     # build the binary
make test      # tests with race detector and coverage
make lint      # golangci-lint
make vet       # go vet
make snapshot  # local release build without publishing (needs goreleaser)
```

CI runs lint, tests, and cross-compilation for every PR; `master` only
changes through PRs with passing checks.

## Releasing (maintainers)

Run the **Release** workflow from the Actions tab with a `vX.Y.Z`
version (it tags `master` and publishes via GoReleaser), or push a
`vX.Y.Z` tag by hand. Tags always carry the `v` prefix.
