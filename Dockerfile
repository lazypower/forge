# syntax=docker/dockerfile:1
ARG GO_IMAGE=docker.io/library/golang:1.26-alpine3.24@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468
ARG RUNTIME_IMAGE=docker.io/library/alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
ARG SOURCE_DATE_EPOCH=0

# Build frontend on the native platform to avoid QEMU-related issues with nodejs ecosystem
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS frontend-build
RUN apk --no-cache add build-base git nodejs pnpm
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY --exclude=.git/ . .
RUN make frontend

# Build backend for each target platform
FROM ${GO_IMAGE} AS build-env

ARG GITEA_VERSION
ARG TAGS=""
ENV TAGS="bindata timetzdata $TAGS"
ARG CGO_EXTRA_CFLAGS

# Build deps
RUN apk --no-cache add \
    build-base \
    git

WORKDIR ${GOPATH}/src/gitea.dev
COPY go.mod go.sum ./
RUN go mod download
# Use COPY instead of bind mount as read-only one breaks makefile state tracking and read-write one needs binary to be moved as it's discarded.
# ".git" directory is mounted separately later only for version data extraction.
COPY --exclude=.git/ . .
COPY --from=frontend-build /src/public/assets public/assets

# Build gitea, .git mount is required for version data
RUN --mount=type=cache,target="/root/.cache/go-build" \
    --mount=type=bind,source=".git/",target=".git/" \
    make backend

COPY docker/root /tmp/local

# Set permissions for builds that made under windows which strips the executable bit from file
RUN chmod 755 /tmp/local/usr/bin/entrypoint \
              /tmp/local/usr/local/bin/* \
              /tmp/local/etc/s6/gitea/* \
              /tmp/local/etc/s6/openssh/* \
              /tmp/local/etc/s6/.s6-svscan/* \
              /go/src/gitea.dev/gitea

FROM ${RUNTIME_IMAGE} AS gitea

ARG GITEA_UPSTREAM_VERSION=1.27.2
ARG GITEA_UPSTREAM_COMMIT
ARG GITEA_PATCH_REVISION
ARG GITEA_SOURCE=https://github.com/go-gitea/gitea

LABEL org.opencontainers.image.source="${GITEA_SOURCE}" \
      org.opencontainers.image.version="${GITEA_UPSTREAM_VERSION}" \
      org.opencontainers.image.revision="${GITEA_PATCH_REVISION}" \
      org.opencontainers.image.base.name="gitea:${GITEA_UPSTREAM_VERSION}@${GITEA_UPSTREAM_COMMIT}" \
      io.gitea.workload-identity.revision="${GITEA_PATCH_REVISION}"

EXPOSE 22 3000

RUN apk --no-cache add \
    bash \
    ca-certificates \
    curl \
    gettext \
    git \
    linux-pam \
    openssh \
    s6 \
    sqlite \
    su-exec \
    gnupg

RUN addgroup \
    -S -g 1000 \
    git && \
  adduser \
    -S -H -D \
    -h /data/git \
    -s /bin/bash \
    -u 1000 \
    -G git \
    git && \
  echo "git:*" | chpasswd -e

COPY --from=build-env /tmp/local /
COPY --from=build-env /go/src/gitea.dev/gitea /app/gitea/gitea

ENV USER=git
ENV GITEA_CUSTOM=/data/gitea

VOLUME ["/data"]

# HINT: HEALTH-CHECK-ENDPOINT: don't use HEALTHCHECK, search this hint keyword for more information
ENTRYPOINT ["/usr/bin/entrypoint"]
CMD ["/usr/bin/s6-svscan", "/etc/s6"]
