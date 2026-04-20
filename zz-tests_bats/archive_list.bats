setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
  require_bin NEBULOUS_BIN nebulous

  # Minimal policy: one split=false text capture. Same shape as the
  # orchestrator.bats fixture.
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

# Pre-populate the archive root with two records (two URLs, one
# policy each). Used by the listing tests below.
seed_two_records() {
  local bin="${NEBULOUS_BIN:-nebulous}"
  run timeout --preserve-status 120s "$bin" archive-capture \
    --policy="$BATS_TEST_TMPDIR/nebulous.toml" \
    --archive-root="$BATS_TEST_TMPDIR/archives" \
    https://example.com/one https://example.com/two
  assert_success
}

run_list() {
  local bin="${NEBULOUS_BIN:-nebulous}"
  run "$bin" archive-list \
    --archive-root="$BATS_TEST_TMPDIR/archives" \
    "$@"
}

# bats file_tags=integration,archive-list

function archive_list_empty_root_is_exit_zero { # @test
  run_list
  assert_success
  [[ -z $output ]] || fail "expected empty output, got: $output"
}

function archive_list_jsonl_emits_one_line_per_record { # @test
  seed_two_records
  run_list --format=jsonl
  assert_success

  # Two records → two JSONL lines.
  local line_count
  line_count=$(echo "$output" | wc -l)
  [[ $line_count -eq 2 ]] || fail "expected 2 lines, got $line_count: $output"

  # Each line is a JSON object with the projection fields.
  echo "$output" | while IFS= read -r line; do
    echo "$line" | jq -e '.subject and .policy_id and .url and .captured_at and (.captures_ok | type == "number") and (.captures_total | type == "number") and .path' >/dev/null ||
      fail "line missing projection field: $line"
  done

  # Both URL subjects appear.
  local subjects
  subjects=$(echo "$output" | jq -r '.subject' | sort | tr '\n' ' ')
  [[ $subjects == *"url:sha256-"* ]] || fail "expected url:* subjects, got: $subjects"
}

function archive_list_table_has_header_and_rows { # @test
  seed_two_records
  run_list --format=table
  assert_success

  echo "$output" | head -1 | grep -q 'SUBJECT' ||
    fail "table missing SUBJECT header: $output"
  echo "$output" | head -1 | grep -q 'CAPTURES' ||
    fail "table missing CAPTURES header: $output"

  # Header + two data rows.
  local line_count
  line_count=$(echo "$output" | wc -l)
  [[ $line_count -eq 3 ]] || fail "expected 3 lines (header + 2), got $line_count: $output"
}

function archive_list_subject_prefix_filters { # @test
  seed_two_records
  # Seed a story-shaped fixture by hand: archive-capture needs a
  # newsblur cache for story IDs, so fake it by writing the archive
  # record directly via a second URL that looks like a story.
  # Actually simpler: archive-list's filter is shape-agnostic, so
  # filter on a URL subject substring the two real records share.
  run_list --format=jsonl "url:"
  assert_success
  local line_count
  line_count=$(echo "$output" | wc -l)
  [[ $line_count -eq 2 ]] || fail "url: prefix should match both seeded records, got $line_count"

  run_list --format=jsonl "story:"
  assert_success
  # No story records were seeded → zero lines.
  [[ -z $output ]] || fail "story: prefix should match zero records, got: $output"
}

function archive_list_rejects_unknown_format { # @test
  run_list --format=xml
  [[ $status -eq 3 ]] || fail "expected exit 3 for unknown format, got $status"
}
