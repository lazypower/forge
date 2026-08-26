# Forge

Forge is a personal project to build and maintain one opinionated software
forge. It originated from Gitea 1.27.2 and is developed independently from that
point.

This repository is not a Gitea distribution, patch queue, or compatibility
project. Gitea is inherited implementation substrate rather than an upstream
product authority. Forge does not merge, rebase onto, or automatically ingest
later Gitea releases. Upstream disclosures may still be consulted when they
identify security or correctness problems in code Forge retains.

Forge is not affiliated with or endorsed by the Gitea project or Gitea Limited.
See [PROVENANCE.md](PROVENANCE.md) for the exact inherited source and attribution.

## Status

Forge is early in its independent lineage. Much of the inherited application,
including the `gitea` binary name, configuration keys, package paths, and user
interface, still identifies itself as Gitea. Those names describe current
implementation compatibility; they do not indicate project affiliation or an
intent to track upstream.

The project is presently maintained for a specific deployment. It may change
APIs, configuration, migrations, workflows, and user-facing behavior when doing
so serves that deployment and the developing human-and-agent collaboration
model.

## Development

Use `make help` to see the available development targets. The inherited build
currently produces a binary named `gitea`:

```sh
make build
./gitea web
```

Development and testing guidance lives in [CONTRIBUTING.md](CONTRIBUTING.md).
Security reports must follow [SECURITY.md](SECURITY.md).

## License

Forge retains Gitea's MIT license and the copyright notices of the Gitea and
Gogs authors. See [LICENSE](LICENSE).

Dependencies and bundled assets retain their own licenses. Release builds
generate a `licenses.txt` artifact containing applicable dependency notices.
