#!/usr/bin/env bash
# Delete the kind cluster. Cached Docker images are kept for next time.
set -euo pipefail
kind delete cluster --name zero-trust-gw
echo "==> Cluster deleted. Images remain in the Docker cache."
