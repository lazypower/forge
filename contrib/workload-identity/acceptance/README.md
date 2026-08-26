# Vault workload identity acceptance

This fixture starts the patched Gitea 1.27.2 image, Gitea Runner 1.0.0,
Vault, and an nginx reverse proxy. Gitea is served from the canonical
`http://proxy:8080/forge/` URL so discovery, JWKS, and token issuance are
tested through a subpath.

Run from the repository root:

```sh
contrib/workload-identity/acceptance/run.sh
```

Requirements are Docker with Compose v2, `curl`, `jq`, and `base64`. The
fixture builds the normal upstream Dockerfile from the current checkout and
deletes all containers and volumes when it exits.

All fixed tokens and passwords are deliberately named `fixture-only`. They
exist only inside the disposable local network and are not suitable for any
deployment. No production credential is read from the environment or stored
in this directory.

The three repositories prove the authorized path, issuance denial without
`id-token: write`, and Vault rejection of a different repository. Gitea Runner
1.0 exposes the GitHub-compatible request variables from its general task
context even when the server omits the permission-gated fields, so the negative
workflow exercises the endpoint and requires a denial. The authorized job also proves wrong-audience and
modified-token rejection and that caller parameters cannot replace identity
claims. A fixture-only probe replays the exact task credential after job
completion to prove issuance is no longer possible.
