# Benchmarks

## Month 2 Brownfield Benchmark Loop

Key files:
- `public-corpus.v1.json` — merged starter corpus
- `brownfield-primary.v1.json` — primary brownfield slice
- `brownfield-holdout.v1.json` — holdout slice
- `bugfix-primary.v1.json` — initial Month 3 bugfix primary slice
- `bugfix-holdout.v1.json` — initial Month 3 bugfix holdout slice
- `month3-canary-corpus.v1.json` — carry-forward Month 2 brownfield canary
- `baseline-plan.v1.json` — starter baseline execution plan
- `month2-brownfield-governance.md` — repo class / holdout / intervention rules
- `month3-bugfix-governance.md` — bugfix slice + canary policy

## CLI commands
- `./elnath eval validate <corpus.json>`
- `./elnath eval summarize <scorecard.json>`
- `./elnath eval diff <current.json> <baseline.json>`
- `./elnath eval report <corpus.json> <current.json> <baseline.json> <output.md>`
- `./elnath eval rules <corpus.json> <scorecard.json>`
- `./elnath eval scaffold-baseline <output.json>`
- `./elnath eval scaffold-current <output.json>`
- `./elnath eval run-baseline <plan.json>`
- `./elnath eval run-current <plan.json>`
- `./elnath eval gate-month2 <corpus.json> <current.json> <baseline.json>`

## End-to-end cycle

The cycle script accepts either an existing current scorecard or a current plan file. If you pass a current plan file, it will generate the current scorecard first via `elnath eval run-current`.

Set `CURRENT_TIMEOUT` / `BASELINE_TIMEOUT` to control wrapper run budgets, and `ELNATH_BENCHMARK_KEEP_TMP=1` to preserve temp repos/logs for debugging.


Use:

```bash
CURRENT_BIN=./scripts/run_current_benchmark_wrapper.sh \
BASELINE_BIN=./scripts/run_baseline_benchmark_wrapper.sh \
BASELINE_TASK_CMD_TEMPLATE='omx run {{task_prompt}}' \
./scripts/run_month2_benchmark_cycle.sh \
  benchmarks/current-plan.v1.json \
  benchmarks/results/cycle-001
```

The wrapper referenced by `BASELINE_BIN` must write one `RunResult` JSON object to the task output path provided by the runner scaffold.

A real baseline wrapper is now provided:

```bash
BASELINE_BIN=./scripts/run_baseline_benchmark_wrapper.sh \
BASELINE_TASK_CMD_TEMPLATE='omx run {{task_prompt}}' \
./elnath eval run-baseline benchmarks/baseline-plan.v1.json
```

For Elnath itself, use the real current-system wrapper or generate a scaffold and adapt it as needed:

```bash
./elnath eval scaffold-current benchmarks/current-plan.v1.json
CURRENT_BIN=./scripts/run_current_benchmark_wrapper.sh \
./elnath eval run-current benchmarks/current-plan.v1.json
```

## Trust boundary

`run-baseline` and the cycle script are **local evaluation tooling**. The plan files, output paths, and `BASELINE_BIN` are treated as trusted local inputs.

## Wrapper skeletons

Starter wrappers are provided for local adaptation:
- `scripts/example_current_benchmark_wrapper.sh`
- `scripts/example_baseline_benchmark_wrapper.sh`

Operational wrappers:
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/run_baseline_benchmark_wrapper.sh`

They intentionally emit placeholder `RunResult` JSON until replaced with a real runner. Use them as contract templates, not as benchmark truth.

The checked-in `scripts/run_current_benchmark_wrapper.sh` clones the repo, runs Elnath on the task prompt, picks a repo-native verification command when possible, and retries once if verification fails.

## Month 3 bugfix cycle

Month 3 keeps the Month 2 smoke set as a carry-forward canary while adding a bugfix wedge.

Use:

```bash
CURRENT_BIN=./scripts/run_current_benchmark_wrapper.sh \
BASELINE_BIN=./scripts/run_baseline_benchmark_wrapper.sh \
BASELINE_TASK_CMD_TEMPLATE='omx exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check {{task_prompt}}' \
./scripts/run_month3_bugfix_cycle.sh \
  benchmarks/bugfix-current-plan.v1.json \
  benchmarks/results/month3-cycle-001
```

This will:
- run the bugfix primary slice
- rerun the Month 2 4-task canary
- fail fast if the canary regresses below baseline on success or verification
