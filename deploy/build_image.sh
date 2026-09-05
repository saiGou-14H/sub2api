#!/usr/bin/env bash
# 本地构建镜像的快速脚本，避免在命令行反复输入构建参数。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

IMAGE_TAG="${IMAGE_TAG:-sub2api:latest}"
IMAGE_REF_PATTERN="${IMAGE_REF_PATTERN:-sub2api:*}"
IMAGE_HISTORY="${SUB2API_IMAGE_HISTORY:-1}"
RUNNING_CONTAINER="${SUB2API_RUNNING_CONTAINER:-sub2api}"

# Keep the running image and one rollback image before creating the next build.
# For a versioned deployment, set IMAGE_REF_PATTERN to the deployment family,
# for example: local/sub2api:web-attachments-9999-*.
"${SCRIPT_DIR}/prune_image_history.sh" \
    "$IMAGE_REF_PATTERN" \
    "$IMAGE_HISTORY" \
    "$RUNNING_CONTAINER"

docker build -t "$IMAGE_TAG" \
    --build-arg GOPROXY=https://goproxy.cn,direct \
    --build-arg GOSUMDB=sum.golang.google.cn \
    -f "${REPO_ROOT}/Dockerfile" \
    "${REPO_ROOT}"
