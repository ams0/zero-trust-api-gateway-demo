#!/usr/bin/env bash
# Pre-pull every image to the local Docker cache. Run this WELL BEFORE the demo
# (it's the only slow, network-bound step). Also pulls the kind node image.
set -euo pipefail
cd "$(dirname "$0")"

# Node image: by default we let `kind create cluster` pull the image bundled with
# your installed kind version (always compatible). Only pre-pull a node image if
# you've explicitly pinned one, e.g.  export KIND_NODE_IMAGE=kindest/node:v1.36.1
# (pick a tag that your kind version actually supports — see `kind` release notes).
if [[ -n "${KIND_NODE_IMAGE:-}" ]]; then
  echo "==> Pulling pinned kind node image: ${KIND_NODE_IMAGE}"
  docker pull "${KIND_NODE_IMAGE}"
else
  echo "==> No KIND_NODE_IMAGE pinned; kind will pull its default node image at"
  echo "    cluster-create time (and cache it). Skipping node-image pre-pull."
fi

echo "==> Pulling workload images from images.txt"
grep -vE '^\s*(#|$)' images.txt | while read -r img; do
  echo "    -> ${img}"
  docker pull "${img}"
done

echo "==> Done. All images cached locally."
