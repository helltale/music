#!/bin/bash
set -eu

export PATH="/opt/bitnami/minio-client/bin:${PATH}"
export MC_CONFIG_DIR=/tmp/mc
mkdir -p "$MC_CONFIG_DIR"

mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
mc mb --ignore-existing local/music
