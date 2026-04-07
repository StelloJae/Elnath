#!/bin/bash
# Elnath Smoke Tests (ST-1 through ST-5)
# Usage: bash scripts/smoke_test.sh
# Requires: ELNATH_ANTHROPIC_API_KEY or ELNATH_OPENAI_API_KEY set

set -euo pipefail

BINARY="./elnath"
PASS=0
FAIL=0
SKIP=0
TEST_DIR=$(mktemp -d)

cleanup() {
    rm -rf "$TEST_DIR"
    # Stop daemon if running
    "$BINARY" daemon stop 2>/dev/null || true
}
trap cleanup EXIT

check_binary() {
    if [[ ! -x "$BINARY" ]]; then
        echo "Binary not found. Run 'make build' first."
        exit 1
    fi
}

check_api_key() {
    if [[ -z "${ELNATH_ANTHROPIC_API_KEY:-}" ]] && [[ -z "${ELNATH_OPENAI_API_KEY:-}" ]]; then
        echo "WARNING: No API key set. LLM-dependent tests will be skipped."
        echo "Set ELNATH_ANTHROPIC_API_KEY or ELNATH_OPENAI_API_KEY to run all tests."
        return 1
    fi
    return 0
}

report() {
    local name="$1" result="$2"
    if [[ "$result" == "PASS" ]]; then
        echo "  [PASS] $name"
        ((PASS++))
    elif [[ "$result" == "SKIP" ]]; then
        echo "  [SKIP] $name"
        ((SKIP++))
    else
        echo "  [FAIL] $name"
        ((FAIL++))
    fi
}

# ============================================================
echo "=== Elnath Smoke Tests ==="
echo ""

check_binary
HAS_API=true
check_api_key || HAS_API=false

# --- ST-0: Build and version ---
echo "--- ST-0: Build sanity ---"

VERSION=$("$BINARY" version 2>&1)
if echo "$VERSION" | grep -q "elnath"; then
    report "version command" "PASS"
else
    report "version command" "FAIL"
fi

HELP=$("$BINARY" help 2>&1)
if echo "$HELP" | grep -q "Commands:"; then
    report "help command" "PASS"
else
    report "help command" "FAIL"
fi

# --- ST-1: End-to-end project creation ---
echo ""
echo "--- ST-1: End-to-end project creation ---"

if [[ "$HAS_API" == "true" ]]; then
    export ELNATH_DATA_DIR="$TEST_DIR/data"
    export ELNATH_WIKI_DIR="$TEST_DIR/wiki"
    mkdir -p "$ELNATH_DATA_DIR" "$ELNATH_WIKI_DIR"

    RESULT=$(echo '새 Go REST API 프로젝트를 '"$TEST_DIR"'/test-api에 만들어줘' | timeout 120 "$BINARY" run 2>&1) || true
    if [[ -f "$TEST_DIR/test-api/main.go" ]]; then
        report "project creation" "PASS"
    else
        report "project creation (file not created, LLM may not have executed tools)" "FAIL"
    fi
else
    report "project creation (no API key)" "SKIP"
fi

# --- ST-2: Wiki knowledge search ---
echo ""
echo "--- ST-2: Wiki knowledge search ---"

WIKI_DIR="$TEST_DIR/wiki-st2"
mkdir -p "$WIKI_DIR"
# Seed wiki with test data
cat > "$WIKI_DIR/test-entry.md" << 'WIKIEOF'
---
title: Stella Changes
type: note
status: published
---

# Stella Changes

On 2026-04-06, the auth module was refactored to support OAuth2.
Commit abc123 updated the middleware chain.
WIKIEOF

export ELNATH_WIKI_DIR="$WIKI_DIR"
export ELNATH_DATA_DIR="$TEST_DIR/data-st2"
mkdir -p "$ELNATH_DATA_DIR"

# Rebuild wiki index
"$BINARY" wiki rebuild 2>&1 || true

SEARCH_RESULT=$("$BINARY" wiki search "Stella 변경" 2>&1) || true
if echo "$SEARCH_RESULT" | grep -qi "stella\|auth\|OAuth2"; then
    report "wiki search" "PASS"
else
    report "wiki search (no results)" "FAIL"
fi

# --- ST-3: Batch job queue ---
echo ""
echo "--- ST-3: Autonomous batch jobs ---"

if [[ "$HAS_API" == "true" ]]; then
    export ELNATH_DATA_DIR="$TEST_DIR/data-st3"
    export ELNATH_WIKI_DIR="$TEST_DIR/wiki-st3"
    mkdir -p "$ELNATH_DATA_DIR" "$ELNATH_WIKI_DIR"

    # Start daemon in background
    "$BINARY" daemon start &
    DAEMON_PID=$!
    sleep 2

    # Submit tasks
    "$BINARY" daemon submit "echo hello from task 1" 2>&1 || true
    "$BINARY" daemon submit "echo hello from task 2" 2>&1 || true
    "$BINARY" daemon submit "echo hello from task 3" 2>&1 || true

    sleep 10

    STATUS=$("$BINARY" daemon status 2>&1) || true
    DONE_COUNT=$(echo "$STATUS" | grep -c "done" || echo "0")

    "$BINARY" daemon stop 2>&1 || true
    wait "$DAEMON_PID" 2>/dev/null || true

    if [[ "$DONE_COUNT" -ge 3 ]]; then
        report "batch 3/3 done" "PASS"
    else
        report "batch jobs ($DONE_COUNT/3 done)" "FAIL"
    fi
else
    report "batch jobs (no API key)" "SKIP"
fi

# --- ST-4: Auto workflow + wiki record ---
echo ""
echo "--- ST-4: Auto workflow + wiki record ---"

if [[ "$HAS_API" == "true" ]]; then
    export ELNATH_DATA_DIR="$TEST_DIR/data-st4"
    export ELNATH_WIKI_DIR="$TEST_DIR/wiki-st4"
    mkdir -p "$ELNATH_DATA_DIR" "$ELNATH_WIKI_DIR"

    RESULT=$(echo '테스트 커버리지 현황을 위키에 정리해줘' | timeout 120 "$BINARY" run 2>&1) || true

    WIKI_CHECK=$("$BINARY" wiki search "테스트 커버리지" 2>&1) || true
    if echo "$WIKI_CHECK" | grep -qi "result\|커버리지\|test"; then
        report "workflow + wiki write" "PASS"
    else
        report "workflow + wiki write (wiki entry not found)" "FAIL"
    fi
else
    report "workflow + wiki write (no API key)" "SKIP"
fi

# --- ST-5: Autoresearch loop ---
echo ""
echo "--- ST-5: Autoresearch loop ---"

if [[ "$HAS_API" == "true" ]]; then
    RESEARCH_WIKI="$TEST_DIR/wiki-st5"
    mkdir -p "$RESEARCH_WIKI"

    export ELNATH_DATA_DIR="$TEST_DIR/data-st5"
    export ELNATH_WIKI_DIR="$RESEARCH_WIKI"
    mkdir -p "$ELNATH_DATA_DIR"

    timeout 180 "$BINARY" run "research --topic 'Go HTTP 성능 최적화' --max-rounds 2" 2>&1 || true

    if [[ -f "$RESEARCH_WIKI/log.md" ]] || ls "$RESEARCH_WIKI"/*.md 1>/dev/null 2>&1; then
        report "autoresearch wiki output" "PASS"
    else
        report "autoresearch wiki output (no files)" "FAIL"
    fi
else
    report "autoresearch (no API key)" "SKIP"
fi

# ============================================================
echo ""
echo "=== Results ==="
echo "  PASS: $PASS"
echo "  FAIL: $FAIL"
echo "  SKIP: $SKIP"
echo ""

if [[ "$FAIL" -gt 0 ]]; then
    echo "SMOKE TESTS: SOME FAILURES"
    exit 1
else
    echo "SMOKE TESTS: ALL PASSED (${SKIP} skipped)"
    exit 0
fi
