# Public Cline fork source research — 2026-10-07

## Reproducible scope

Read-only research run: https://github.com/1057300248/sub2api/actions/runs/37609120464
Artifact: `cline-fork-source-research`, SHA-256 `7cddb57fb7724c3f6900736eb411974c7ed43e678a884af000961cc34cbbb99c`.

Inventory: 119 direct `ranxi2001/sub2api` forks plus the 500 newest `Wei-Shaw/sub2api` forks, and five explicitly selected repositories. 624 repository references attempted; 619 returned heads, five failed. Default heads and branches containing `cline` were selected; identical commits were deduplicated into 191 heads. Five known Cline/Chat/rate-limit source paths were checked at exact SHAs. No third-party code was executed.

This is not every historical fork, every branch or a full-file scan of every repository. Code in differently named files, older unselected forks and inaccessible repositories can be missed. Repository README search and GitHub code indexing were used as additional discovery, not as proof of absence.

## Confirmed implementation

Repository: https://github.com/giscatgis-lang/sub2api-cline
Inspected head: `11020fed3565c3178a32512da9f1c0b3821daf4c`.
Primary source: `backend/internal/service/openai_gateway_chat_completions_raw.go`.

Implemented: recognizing api.cline.bot for OpenAI API-key accounts; unwrapping successful JSON envelopes; normalizing non-stream Chat and SSE completion metadata; preserving selected usage/tool-call/reasoning fields. This is real compatibility code, not just a repository name.

Limitations observed in source:

- No Cline-specific weekly/monthly reset handler in its rate-limit service.
- The detector targets OpenAI API-key accounts, not an independent Cline platform with separate Pass/free/paid entitlements.
- `normalizeSSELine` selects a field allowlist, does not preserve a top-level error, and unconditionally labels the result a Chat completion chunk. An error-only frame can therefore lose its failure signal. This is a source-level finding, not a live service test.
- The stream forwarding call site logs normalizer errors and proceeds; it is not a complete successful-terminal/failed-generation guard.

Decision: retain the useful envelope/SSE normalization ideas in the already guarded implementation, but do not transplant the older router wholesale or copy error-dropping normalization. Our Cline failure and usage-boundary regression tests remain required.

No other external implementation matched the five inspected source paths in this bounded scan. This is evidence for the inspected scope, not a claim that no other Cline fork exists anywhere. The full per-repository heads, source hashes, match lines and failures are in the artifact.
