#!/usr/bin/env bash
# Build the instrumented echo service image and load it into the kind cluster.
# Uses the save --platform | load image-archive path (works with Docker Desktop's
# containerd image store; see README step 3).
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER="zero-trust-gw"
IMG="echo-svc:demo"
ARCH="linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"

echo "==> Building ${IMG} for ${ARCH}"
docker build --platform "${ARCH}" -t "${IMG}" apps/echo-svc

echo "==> Loading ${IMG} into kind cluster '${CLUSTER}'"
docker image save --platform "${ARCH}" "${IMG}" -o /tmp/echo-svc.tar
kind load image-archive /tmp/echo-svc.tar --name "${CLUSTER}"
rm -f /tmp/echo-svc.tar

echo "==> Done. Image ${IMG} is available in the cluster."
