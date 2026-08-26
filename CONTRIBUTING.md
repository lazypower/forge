# Contributing to Forge

Forge is a personal, independently maintained software forge. Contributions are
welcome for consideration, but the project does not promise support, review, or
merge timelines.

Before proposing a substantial change, open an issue describing the problem,
the desired behavior, and why the change belongs in Forge. Design decisions are
made for Forge's own product direction; compatibility with later Gitea releases
is not a goal.

Security reports must follow [SECURITY.md](SECURITY.md) and must not be filed as
public issues.

## Development

| Topic                  | Document                                                         |
|:-----------------------|:-----------------------------------------------------------------|
| Setup and requirements | [docs/build-setup.md](docs/build-setup.md)                       |
| Development workflow   | [docs/development.md](docs/development.md)                       |
| Build from source      | [docs/build-source.md](docs/build-source.md)                     |
| Running tests          | [docs/testing.md](docs/testing.md)                               |
| Frontend guidelines    | [docs/guidelines-frontend.md](docs/guidelines-frontend.md)       |
| Backend guidelines     | [docs/guidelines-backend.md](docs/guidelines-backend.md)         |
| Refactoring            | [docs/guidelines-refactoring.md](docs/guidelines-refactoring.md) |
| Project governance     | [docs/community-governance.md](docs/community-governance.md)     |

Use `make help` to discover supported development targets. In particular:

- Run `make fmt` after changing Go files.
- Run `make lint-go` for Go changes.
- Run `make lint-js` for TypeScript changes.
- Run `make tidy` after changing `go.mod`.
- Prefer focused unit tests before broader suites.

Pull requests should be small enough to review coherently and should not mix
unrelated cleanup with the intended change. Titles and commits use Conventional
Commits, for example `fix(repo): reject invalid object names`.

## Human and agent contributions

Humans and software agents may both participate in Forge development. The human
submitting a contribution remains responsible for its intent, accuracy,
licensing, tests, and review responses.

Disclose material agent assistance in the pull request and preserve applicable
authorship or assistance trailers. An agent cannot make legal certifications on
behalf of a person.

## Copyright and licensing

Do not remove or rewrite copyright, license, provenance, or attribution notices
that still apply. New Go files must include the current year and use:

```go
// Copyright <year> The Forge Authors. All rights reserved.
// SPDX-License-Identifier: MIT
```

Contributions are accepted under the repository's [MIT license](LICENSE). By
submitting a pull request, the human submitter certifies the terms of the
[Developer Certificate of Origin](DCO). A separate copyright assignment is not
required.
