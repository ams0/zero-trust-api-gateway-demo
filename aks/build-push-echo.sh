#!/usr/bin/env bash
# Build the instrumented echo service and push it to Docker Hub for AKS to pull.
# (kind side-loads the image; a cloud cluster must pull it from a registry.)
# AKS nodepool1 is amd64, so a single linux/amd64 image is enough.
#
#   docker login                     # if not already logged in to docker.io
#   ./aks/build-push-echo.sh
set -euo pipefail
cd "$(dirname "$0")/.."

IMG="${ECHO_IMAGE:-docker.io/ams0/echo-svc:demo}"
PLATFORM="${ECHO_PLATFORM:-linux/amd64}"

echo "==> Building ${IMG} for ${PLATFORM}"
docker build --platform "${PLATFORM}" -t "${IMG}" apps/echo-svc

echo "==> Pushing ${IMG}"
docker push "${IMG}"

echo "==> Restarting orders/inventory to pull the new image"
kubectl rollout restart deploy/orders deploy/inventory -n apps 2>/dev/null || true
echo "==> Done."
