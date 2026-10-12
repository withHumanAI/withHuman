# Contributing to withHuman

Thanks for helping improve withHuman. This guide covers how to report
problems, set up the code, and send a change.

## Report a problem or suggest a feature

- **Bugs and feature ideas:** open an issue using one of the templates.
  For anything larger than a small fix, open an issue first so we can
  agree on the approach before you write code.
- **Security vulnerabilities:** never open a public issue. Follow
  [SECURITY.md](SECURITY.md) instead.
- **The hosted edition at withhuman.ai:** contact
  [withHuman support](https://withhuman.ai/support).

## Set up the code

You need Go (the version in `go.mod`) and Node 25.

```sh
make frontend
go run ./cmd/withhuman
```

Open [http://localhost:8080](http://localhost:8080) and create the first
owner with the bootstrap secret from `~/.withhuman/bootstrap-secret`. The
[README](README.md) covers configuration.

## Check your change

```sh
make test     # Go tests and vet, plus the frontend tests
make verify   # also lints the OpenAPI contract and the deployment files
make smoke    # runs one real approval against a server you started
```

`make test` skips nothing by default, so the first run downloads PostgreSQL
for the integration tests. Pass `GOTESTFLAGS=-short` to skip them while you
iterate. Use the Make targets rather than plain `go build` on a clean
checkout: they build the frontend the binary embeds.

## Write the change

- Follow [CODING_CONVENTIONS.md](CODING_CONVENTIONS.md) for naming, errors,
  logging and API shape.
- [CLAUDE.md](CLAUDE.md) holds the architecture rules and how each part of
  the system works. Coding agents read it too, so it stays current.
- Add or update tests for the behaviour you change.
- Never edit a migration that has shipped in a release. Add a new numbered
  file under `db/migrations` instead.
- When a change affects what users see or do, update the
  [CLI guide](deployment/cli/README.md) or the agent skills in
  [`skills/`](skills) in the same pull request.
- Product copy says what something is or what to do, in one short
  sentence, and never uses em dashes. `CLAUDE.md` has the full rules.

## Send a pull request

- Keep each pull request to one change, and fill in the template: the
  problem in one sentence, then the solution.
- Write commit messages as a short sentence in the imperative, such as
  "Cap hover cards at the viewport height".
- CI must pass before a pull request is merged.
- You must be able to stand behind every line you submit. Don't credit AI
  agents in commits or pull requests: no co-author trailers, "generated
  with" lines or agent signatures.

## Code of conduct

Everyone taking part in this project follows our
[code of conduct](CODE_OF_CONDUCT.md).
