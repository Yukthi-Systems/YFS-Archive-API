## Summary

<!-- What does this PR change, and why? Keep it short. -->

Closes #

## Type of change

- [ ] 🐛 Bug fix (non-breaking change that fixes an issue)
- [ ] ✨ New feature (non-breaking change that adds functionality)
- [ ] 💥 Breaking change (API, config, schema expectations or archive output change)
- [ ] ♻️ Refactor (no behavior change)
- [ ] 📝 Documentation only
- [ ] 🔧 Build / CI / dependencies

## How was this tested?

<!-- Commands run, test cases added, manual steps (e.g. curl flow, archive extracted on macOS/Windows). -->

## Checklist

- [ ] `gofmt -l .` prints nothing
- [ ] `go vet ./...` and `go build ./...` pass
- [ ] `go test -race ./...` passes, and tests are added or updated where it makes sense
- [ ] New or changed functions have Go doc comments
- [ ] `internal/repository` still issues only `SELECT` statements
- [ ] File and archive bytes are streamed, never fully buffered in memory
- [ ] README.md / DOCKERHUB.md / `.env.example` are updated if config, the API or deployment changed
- [ ] No secrets, credentials or real hostnames are included
- [ ] Commits follow [Conventional Commits](https://www.conventionalcommits.org/)

## Breaking changes / migration notes

<!-- Delete if not applicable. What must operators or API consumers change? -->

## Screenshots / logs

<!-- Optional. Redact tokens and credentials. -->
