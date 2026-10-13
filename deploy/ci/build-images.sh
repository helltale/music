#!/bin/bash
# Build each application image once and tag it git-<sha>.
# Stage and production pull this tag. They do not build again.
set -eu

sha="${GITHUB_SHA:-$(git rev-parse HEAD)}"
repository="${GITHUB_REPOSITORY:-helltale/music}"
owner=$(printf '%s' "${GITHUB_REPOSITORY_OWNER:-helltale}" | tr '[:upper:]' '[:lower:]')
registry="ghcr.io/${owner}"
tag="git-${sha}"
source_url="https://github.com/${repository}"

build() {
  name="$1"
  dockerfile="$2"
  image="${registry}/music-${name}:${tag}"
  docker build \
    --label "org.opencontainers.image.source=${source_url}" \
    --label "org.opencontainers.image.revision=${sha}" \
    --label "org.opencontainers.image.version=${tag}" \
    -f "$dockerfile" \
    -t "$image" \
    .
  if [ "${PUSH_IMAGES:-false}" = "true" ]; then
    docker push "$image"
    docker image inspect --format '{{index .RepoDigests 0}}' "$image"
  fi
  printf '%s\n' "$image"
}

root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"

build api backend/Dockerfile.api
build worker backend/Dockerfile.worker
build web frontend/Dockerfile
