#! /bin/bash -e

# Shared setup for nebulous bats tests. Mirrors madder's pattern: isolate
# HOME + XDG via bats-island, bound the madder config walk so the user's
# real ~/.madder can't bleed into tests, and resolve binaries via
# *_BIN env vars with PATH fallback.

if [[ -z $BATS_TEST_TMPDIR ]]; then
  echo 'common.bash loaded before $BATS_TEST_TMPDIR set. aborting.' >&2

  cat >&2 <<-'EOM'
    only load this file from `.bats` files like so:

    setup() {
      load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"

      # for shellcheck SC2154
      export output
    }

    as there is a hard assumption on $BATS_TEST_TMPDIR being set
EOM

  exit 1
fi

pushd "$BATS_TEST_TMPDIR" >/dev/null || exit 1

bats_load_library bats-support
bats_load_library bats-assert
bats_load_library bats-emo
bats_load_library bats-island

setup_test_home

# Bound madder's upward walk for `.madder` config so ancestor dirs (the
# user's $HOME/.madder, etc.) can't inject config into tests.
export MADDER_CEILING_DIRECTORIES="$BATS_TEST_TMPDIR"

require_bin MIGRATE_CACHE_BIN migrate-cache
require_bin MADDER_BIN madder

run_migrate_cache() {
  local bin="${MIGRATE_CACHE_BIN:-migrate-cache}"
  run timeout --preserve-status 30s "$bin" "$@"
}

run_madder() {
  local bin="${MADDER_BIN:-madder}"
  run timeout --preserve-status 5s "$bin" "$@"
}

# write_legacy_manifest emits a JSON blobstore.Manifest with a single
# entry pointing at a blob file created alongside. Args:
#   $1 logical cache key (any string)
#   $2 blob body
# After return:
#   $HOME/.cache/nebulous/store/manifest.json contains the entry
#   $HOME/.cache/nebulous/store/blobs/sha256/<sha256> contains the body
#   $BLOB_SHA256 is exported with the computed digest
write_legacy_manifest() {
  local key="$1"
  local body="$2"

  local legacy_dir="$HOME/.cache/nebulous/store"
  local blob_dir="$legacy_dir/blobs/sha256"
  mkdir -p "$blob_dir"

  BLOB_SHA256=$(printf '%s' "$body" | sha256sum | awk '{print $1}')
  export BLOB_SHA256

  printf '%s' "$body" >"$blob_dir/$BLOB_SHA256"

  cat >"$legacy_dir/manifest.json" <<EOF
{
  "entries": {
    "$key": {
      "digest": "$BLOB_SHA256",
      "written_at": "2026-01-01T00:00:00Z",
      "immutable": true
    }
  }
}
EOF
}
