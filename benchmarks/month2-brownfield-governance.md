# Month 2 Brownfield Corpus Governance

## Repo class inclusion rule
Month 2 primary brownfield tasks must satisfy all of the following:
- existing public repo with stable source URL
- deterministic tests already present or easily runnable
- clear module boundaries that make file selection auditable
- small-to-medium CLI/developer-tool repo or conventional service/backend repo

Repos are excluded from the primary slice when they are:
- framework-heavy or monorepo-first
- dependent on large external infra for basic task verification
- too toy-like to predict real brownfield superiority

## Holdout slice rule
- Keep at least **25%** of brownfield tasks in a holdout slice.
- Holdout tasks must come from the same broad repo classes but different repos or different task families.
- Do not tune prompts/routing heuristics only against the holdout slice after it is designated.
- Month 2 separation claims require both primary-cycle improvement and holdout sanity checks.

## Intervention scoring rule
For each task run, classify interventions as:
- **necessary**: missing info would materially change outcome or involves expensive / irreversible choice
- **avoidable**: answer was discoverable from repo/context/tools
- **late**: question came after the system had already gone down the wrong path

Month 2 goal is not zero questions; it is a higher **necessity-weighted** question quality and fewer avoidable/late interruptions.
