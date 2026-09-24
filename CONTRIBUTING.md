# Contributing to YFS Archive API

Thanks for your interest in improving YFS Archive API! This guide explains
how to propose changes and what reviewers look for.

By participating you agree to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- 🐛 **Report a bug.** [Open a bug report](https://github.com/Yukthi-Systems/YFS-Archive-API/issues/new?template=bug_report.yml).
- 💡 **Suggest a feature.** [Open a feature request](https://github.com/Yukthi-Systems/YFS-Archive-API/issues/new?template=feature_request.yml).
- 📝 **Improve the docs.** Typos, clarifications and examples are always welcome.
- 🔧 **Send code.** Look for issues labelled `good first issue` or `help wanted`.
- 🔒 **Report a vulnerability.** Please follow [SECURITY.md](SECURITY.md), not a public issue.

Questions? Ask on <a href="https://discord.com/invite/2BS7Z4FhJ" target="_blank" rel="noopener noreferrer">Discord</a>
or email [connect@yukthi.com](mailto:connect@yukthi.com).

## Before you start

For anything larger than a small fix, please **open an issue first** and
describe what you want to change. That way we can agree on the approach
before you invest time, especially for changes to the public API, the
database queries or the archive format.

## Development setup

**Requirements:** Go 1.25+, and a PostgreSQL instance with the
[expected schema](README.md#database-schema). Docker is optional.

```bash
git clone https://github.com/<your-username>/YFS-Archive-API.git
cd YFS-Archive-API
git remote add upstream https://github.com/Yukthi-Systems/YFS-Archive-API.git
go mod download
cp .env.example .env    # point DATABASE_URL at your dev database
go run ./cmd/server
```

## Workflow

1. Sync with upstream: `git fetch upstream && git switch -c feat/short-description upstream/main`
2. Make your change in small, focused commits.
3. Run the checks below. CI runs the same ones.
4. Push to your fork and open a pull request against `main`.
5. Fill in the pull request template and link the issue (`Closes #123`).

### Branch names

`feat/…`, `fix/…`, `docs/…`, `refactor/…`, `chore/…`

### Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat(archive): support tar.gz export type
fix(sse): stop stream when job is deleted mid-poll
docs: document storage server contract
```

## Checks

All of these must pass before a PR can be merged:

```bash
gofmt -l .            # must print nothing
go vet ./...
go build ./...
go test -race ./...
```

## Code guidelines

- **Follow the layering.** `handler → service → repository / storage /
  client / archive`. Handlers never run SQL or touch the filesystem.
  Only `internal/config` reads environment variables.
- **Document everything.** Every package, exported identifier and function
  gets a Go doc comment that starts with its name, as `go doc` expects.
- **Keep it read-only.** `internal/repository` must only issue `SELECT`
  statements. This service never writes to the database.
- **Stream, don't buffer.** File and archive bytes must flow through
  `io.Reader` / `io.Writer`. Never read a whole file into memory.
- **Keep errors safe.** Return generic messages to clients, and log the
  detail with `slog`.
- **Depend on interfaces.** New backends (Redis, S3, …) implement the
  existing `job.Store`, `token.Service` and `storage.ArchiveStorage`
  interfaces.
- **Add dependencies sparingly.** Prefer the standard library, and explain
  any new module in the PR.
- **Add tests with your change.** Put them next to the code
  (`foo_test.go`) and prefer table-driven tests.

## Documentation

If your change affects configuration, the API, the schema or deployment,
update [README.md](README.md) and, if it affects the image,
[DOCKERHUB.md](DOCKERHUB.md) in the same PR.

## License

By contributing, you agree that your contributions will be licensed under
the [GNU General Public License v3.0](LICENSE), the same license as the
project.
