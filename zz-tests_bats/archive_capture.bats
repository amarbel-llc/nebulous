setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output

  # The capturer from the flake devShell. Mirrors MADDER_BIN / MIGRATE_CACHE_BIN.
  require_bin CHREST_BIN chrest

  # Minimal writer-protocol stub (RFC 0001 § Writer Protocol). Reads
  # stdin, emits one NDJSON object with id + size. Keeps the test
  # hermetic — no dependency on a running madder.
  cat >"$BATS_TEST_TMPDIR/writer-stub.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
size=$(wc -c)
printf '{"id":"blake2b256-stub-%s","size":%s}\n' "$size" "$size"
SH
  chmod +x "$BATS_TEST_TMPDIR/writer-stub.sh"

  # Batch input fixture. Uses firefox backend (Chrome fails on kernel
  # 6.17+ per chrest#10 notes). split=false keeps MVP scope.
  cat >"$BATS_TEST_TMPDIR/batch-input.json" <<JSON
{
  "schema": "web-capture-archive/v1",
  "writer": { "cmd": ["$BATS_TEST_TMPDIR/writer-stub.sh"] },
  "url": "https://example.com/",
  "defaults": { "browser": "firefox", "isolation": "fresh", "split": false },
  "captures": [
    { "name": "text",       "format": "text" },
    { "name": "screenshot", "format": "screenshot",
      "options": { "format": "png", "full-page": true } },
    { "name": "pdf",        "format": "pdf",
      "options": { "background": true, "landscape": false } }
  ]
}
JSON
}

run_capture_batch() {
  local bin="${CHREST_BIN:-chrest}"
  run timeout --preserve-status 120s bash -c \
    "\"$bin\" capture-batch < \"$BATS_TEST_TMPDIR/batch-input.json\" 2>\"$BATS_TEST_TMPDIR/chrest.err\""
}

# bats file_tags=integration,archive

function capture_batch_split_false_produces_rfc_shape { # @test
  run_capture_batch
  assert_success

  # One JSON object per § Capturer Protocol Batch Output.
  assert_equal "$(echo "$output" | jq -r '.schema')"        'web-capture-archive/v1'
  assert_equal "$(echo "$output" | jq -r '.capturer.name')" 'chrest'
  assert_equal "$(echo "$output" | jq    '.errors | length')" '0'
  assert_equal "$(echo "$output" | jq    '.captures | length')" '3'

  # Every capture has a spec + payload (split=false so no envelope).
  local missing_payload
  missing_payload=$(echo "$output" | jq '[.captures[] | select(.payload == null)] | length')
  assert_equal "$missing_payload" '0'

  local missing_spec
  missing_spec=$(echo "$output" | jq '[.captures[] | select(.spec == null)] | length')
  assert_equal "$missing_spec" '0'

  local has_envelope
  has_envelope=$(echo "$output" | jq '[.captures[] | select(.envelope != null)] | length')
  assert_equal "$has_envelope" '0'

  # No per-capture errors.
  local per_capture_errors
  per_capture_errors=$(echo "$output" | jq '[.captures[] | select(.error != null)] | length')
  assert_equal "$per_capture_errors" '0'
}

function capture_batch_media_types_per_format { # @test
  run_capture_batch
  assert_success

  assert_equal "$(echo "$output" | jq -r '.captures[] | select(.name=="text")       | .payload.media_type')" 'text/plain; charset=utf-8'
  assert_equal "$(echo "$output" | jq -r '.captures[] | select(.name=="screenshot") | .payload.media_type')" 'image/png'
  assert_equal "$(echo "$output" | jq -r '.captures[] | select(.name=="pdf")        | .payload.media_type')" 'application/pdf'

  local spec_media
  spec_media=$(echo "$output" | jq -r '.captures[0].spec.media_type')
  assert_equal "$spec_media" 'application/vnd.web-capture-archive.spec+json'
}

function capture_batch_rejects_unknown_schema { # @test
  cat >"$BATS_TEST_TMPDIR/bad-batch.json" <<JSON
{
  "schema": "bogus/v99",
  "writer": { "cmd": ["$BATS_TEST_TMPDIR/writer-stub.sh"] },
  "url": "https://example.com/",
  "captures": [{ "name": "text", "format": "text" }]
}
JSON

  local bin="${CHREST_BIN:-chrest}"
  run bash -c \
    "\"$bin\" capture-batch < \"$BATS_TEST_TMPDIR/bad-batch.json\" 2>/dev/null"

  # RFC § Batch Input: MUST exit non-zero without writing batch output.
  [[ "$status" -ne 0 ]] || fail "expected non-zero exit for unknown schema, got $status"
}
