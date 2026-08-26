set shell := ["sh", "-eu", "-c"]

default:
    @just --list

# Show the inherited lineage and current fork revision.
status:
    @contrib/workload-identity/maintenance/status.sh

# Build the exact rootless linux/amd64 image.
build:
    @contrib/workload-identity/image/build.sh

# Lint, build twice, smoke test, and run Vault acceptance.
test:
    @contrib/workload-identity/ci/verify-fork.sh

# Tag and publish a fully verified revision to GitHub.
push remote="origin":
    @contrib/workload-identity/maintenance/push.sh "{{ remote }}"
