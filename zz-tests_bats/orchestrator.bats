setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
  require_bin NEBULOUS_BIN nebulous

  # Minimal policy: one split=false text capture against a URL
  # provided as a positional target (no newsblur cache needed).
  cat >"$BATS_TEST_TMPDIR/nebulous.toml" <<'TOML'
[[policy]]
id        = "default"
url       = "{{.Story.Permalink}}"
isolation = "fresh"

[[policy.capture]]
name    = "text"
format  = "text"
browser = "firefox"
TOML

  mkdir -p "$BATS_TEST_TMPDIR/archives"
}

# Flags must appear before positional targets: Go's flag package
# stops parsing at the first non-flag arg.
run_archive() {
  local bin="${NEBULOUS_BIN:-nebulous}"
  run timeout --preserve-status 120s "$bin" archive-capture \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives" \
    "$@"
}

run_archive_stdin() {
  local bin="${NEBULOUS_BIN:-nebulous}"
  run timeout --preserve-status 120s "$bin" archive-capture \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives" \
    - <<<"$1"
}

# bats file_tags=integration,orchestrator

function orchestrator_url_mode_produces_record_file { # @test
  run_archive https://example.com/
  assert_success

  # stdout is non-tty JSON here (bats captures it); parse with jq.
  assert_equal "$(echo "$output" | jq -r '.bailed_out')" 'false'
  assert_equal "$(echo "$output" | jq '.written | length')" '1'
  assert_equal "$(echo "$output" | jq '.failed | length')" '0'

  local pth
  pth=$(echo "$output" | jq -r '.written[0].path')
  [[ -f $pth ]] || fail "record file not at $pth"

  # The record's schema + policy_id came through correctly.
  assert_equal "$(jq -r '.schema' "$pth")" 'web-capture-archive.record/v1'
  assert_equal "$(jq -r '.policy_id' "$pth")" 'default'
}

function orchestrator_stdin_batch_produces_records_per_target { # @test
  # Two URLs via stdin, `-` sentinel. Each becomes its own subject,
  # so we expect two written records.
  run_archive_stdin "$(printf 'https://example.com/one\nhttps://example.com/two\n')"
  assert_success

  assert_equal "$(echo "$output" | jq -r '.bailed_out')" 'false'
  assert_equal "$(echo "$output" | jq '.written | length')" '2'
  assert_equal "$(echo "$output" | jq '.failed | length')" '0'
}

function orchestrator_rejects_missing_targets { # @test
  local bin="${NEBULOUS_BIN:-nebulous}"
  run "$bin" archive-capture \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives"
  [[ $status -eq 3 ]] || fail "expected exit 3 (no targets), got $status"
}

function orchestrator_rejects_unclassifiable_positional { # @test
  local bin="${NEBULOUS_BIN:-nebulous}"
  run "$bin" archive-capture \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives" \
    "not-a-url-or-story"
  [[ $status -eq 3 ]] || fail "expected exit 3 (unclassifiable target), got $status"
}

function orchestrator_rejects_missing_policy_file { # @test
  local bin="${NEBULOUS_BIN:-nebulous}"
  run "$bin" archive-capture \
    --policy="$BATS_TEST_TMPDIR/does-not-exist.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives" \
    https://example.com/

  # Policy-load failure is a pre-job error → Report has one Failed
  # entry with kind=policy-load-failed, ExitCode=1 (mixed, no writes).
  [[ $status -eq 1 ]] || fail "expected exit 1 (policy-load failed), got $status"
  assert_equal "$(echo "$output" | jq '.failed | length')" '1'
  assert_equal "$(echo "$output" | jq -r '.failed[0].kind')" 'policy-load-failed'
}
