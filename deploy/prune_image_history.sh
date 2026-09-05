#!/usr/bin/env bash
# Remove stale images from one deployment image family.
#
# The running image and KEEP_HISTORY previous images are preserved. This keeps
# one rollback point without touching images from other repositories/tags.

set -euo pipefail

IMAGE_REF_PATTERN="${1:-${SUB2API_IMAGE_REF_PATTERN:-sub2api:*}}"
KEEP_HISTORY="${2:-${SUB2API_IMAGE_HISTORY:-1}}"
RUNNING_CONTAINER="${3:-${SUB2API_RUNNING_CONTAINER:-sub2api}}"

if ! [[ "$KEEP_HISTORY" =~ ^[0-9]+$ ]]; then
    printf 'image history count must be a non-negative integer: %s\n' "$KEEP_HISTORY" >&2
    exit 2
fi

mapfile -t candidates < <(
    docker image ls \
        --format '{{.Repository}}:{{.Tag}}' \
        --filter "reference=${IMAGE_REF_PATTERN}" |
        sort -u
)

if ((${#candidates[@]} == 0)); then
    printf '[image-retention] no images matched %s\n' "$IMAGE_REF_PATTERN"
    exit 0
fi

running_image=""
if [[ -n "$RUNNING_CONTAINER" ]]; then
    running_image="$(docker inspect -f '{{.Config.Image}}' "$RUNNING_CONTAINER" 2>/dev/null || true)"
fi

declare -A keep=()
if [[ -n "$running_image" ]]; then
    for image in "${candidates[@]}"; do
        if [[ "$image" == "$running_image" ]]; then
            keep["$image"]=1
            printf '[image-retention] keeping running image %s\n' "$image"
            break
        fi
    done
fi

# Sort by Docker's immutable creation timestamp, newest first. The running
# image is already protected; the next KEEP_HISTORY images become rollback
# candidates.
mapfile -t ordered < <(
    for image in "${candidates[@]}"; do
        created="$(docker image inspect -f '{{.Created}}' "$image")"
        printf '%s\t%s\n' "$created" "$image"
    done |
        sort -r
)

history_kept=0
for entry in "${ordered[@]}"; do
    image="${entry#*$'\t'}"
    if [[ -n "${keep[$image]+x}" ]]; then
        continue
    fi
    if ((history_kept < KEEP_HISTORY)); then
        keep["$image"]=1
        history_kept=$((history_kept + 1))
        printf '[image-retention] keeping rollback image %s\n' "$image"
        continue
    fi
    printf '[image-retention] removing stale image %s\n' "$image"
    docker image rm "$image" >/dev/null
done
