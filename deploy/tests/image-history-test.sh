#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

cat >"$test_root/docker" <<'EOF'
#!/bin/sh
set -eu

case "$*" in
  "image ls --format {{.Repository}}:{{.Tag}} --filter reference=example/sub2api:*")
    printf '%s\n' \
      'example/sub2api:oldest' \
      'example/sub2api:rollback' \
      'example/sub2api:current' \
      'example/sub2api:stale'
    ;;
  "inspect -f {{.Config.Image}} sub2api-test")
    printf '%s\n' 'example/sub2api:current'
    ;;
  "image inspect -f {{.Created}} example/sub2api:oldest")
    printf '%s\n' '2026-01-01T00:00:00Z'
    ;;
  "image inspect -f {{.Created}} example/sub2api:rollback")
    printf '%s\n' '2026-02-01T00:00:00Z'
    ;;
  "image inspect -f {{.Created}} example/sub2api:current")
    printf '%s\n' '2026-03-01T00:00:00Z'
    ;;
  "image inspect -f {{.Created}} example/sub2api:stale")
    printf '%s\n' '2026-01-15T00:00:00Z'
    ;;
  "image rm example/sub2api:oldest"|"image rm example/sub2api:stale")
    printf '%s\n' "${3}" >>"$MOCK_REMOVED"
    ;;
  *)
    printf 'unexpected docker invocation: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$test_root/docker"
removed_file="$test_root/removed"
export MOCK_REMOVED="$removed_file"
export PATH="$test_root:$PATH"

output=$(bash "$repo_root/deploy/prune_image_history.sh" \
  'example/sub2api:*' 1 sub2api-test)

grep -Fq 'keeping running image example/sub2api:current' <<EOF
$output
EOF
grep -Fq 'keeping rollback image example/sub2api:rollback' <<EOF
$output
EOF
grep -Fq 'removing stale image example/sub2api:oldest' <<EOF
$output
EOF
test -e "$removed_file"
grep -Fq 'example/sub2api:oldest' "$removed_file"
grep -Fq 'example/sub2api:stale' "$removed_file"

printf 'image history retention test passed\n'
