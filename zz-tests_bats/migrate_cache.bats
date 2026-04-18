setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=migrate

function migrate_cache_no_legacy_manifest_exits_clean { # @test
  run_migrate_cache
  assert_success
  assert_output --partial "no legacy manifest"
}

function migrate_cache_roundtrip_single_entry { # @test
  local key="testkey00000000000000000000000000000000000000000000000000000000"
  local body="hello world"

  write_legacy_manifest "$key" "$body"

  run_migrate_cache
  assert_success
  assert_output --partial "migrated 1 entries"

  local new_manifest="$XDG_DATA_HOME/nebulous/manifest.json"
  [[ -f $new_manifest ]] || fail "new manifest missing at $new_manifest"

  local new_digest
  new_digest=$(jq -r ".entries[\"$key\"].digest" "$new_manifest")
  [[ -n $new_digest && $new_digest != "null" ]] || fail "new manifest has no entry for $key"
  [[ $new_digest == blake2b256-* ]] || fail "expected blake2b256- prefix, got: $new_digest"

  run_madder cat nebulous "$new_digest"
  assert_success
  assert_output --partial "$body"
}

function migrate_cache_is_idempotent { # @test
  local key="testkey00000000000000000000000000000000000000000000000000000000"
  local body="idempotent body"

  write_legacy_manifest "$key" "$body"

  run_migrate_cache
  assert_success

  local first_digest
  first_digest=$(jq -r ".entries[\"$key\"].digest" "$XDG_DATA_HOME/nebulous/manifest.json")

  run_migrate_cache
  assert_success
  assert_output --partial "skipped 1"

  local second_digest
  second_digest=$(jq -r ".entries[\"$key\"].digest" "$XDG_DATA_HOME/nebulous/manifest.json")
  [[ $first_digest == "$second_digest" ]] || fail "digest changed on re-run: $first_digest -> $second_digest"
}

function migrate_cache_dry_run_touches_nothing { # @test
  local key="testkey00000000000000000000000000000000000000000000000000000000"
  local body="dry run body"

  write_legacy_manifest "$key" "$body"

  run_migrate_cache -dry-run
  assert_success
  assert_output --partial "migrated 1 entries"

  [[ ! -e "$XDG_DATA_HOME/nebulous/manifest.json" ]] || fail "dry-run wrote new manifest"
}
