set shell := ["sh", "-eu", "-c"]

default:
    @just --list

# Show the pinned upstream and current patch revision.
status:
    @contrib/workload-identity/maintenance/status.sh

# Replay this patch set onto an upstream Gitea release.
update version:
    @contrib/workload-identity/maintenance/update.sh "{{ version }}"

# Build the exact rootless linux/amd64 image.
build:
    @contrib/workload-identity/image/build.sh

# Replay, lint, build twice, smoke test, and run Vault acceptance.
test:
    @contrib/workload-identity/ci/verify-update.sh

# Tag and publish a fully verified revision to GitHub.
push remote="origin":
    @contrib/workload-identity/maintenance/push.sh "{{ remote }}"
