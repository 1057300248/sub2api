# Cline-only v2.9.11 review increment

## Source and rollback boundaries

Upstream is pinned to `ranxi2001/sub2api` v2.9.11, commit
`e6addfe4aff745c859f9bb50817bc468e0e1f4f7`. The preserved PR #13 rollback
point is `0d923dc088c665cdbf268ad524160f0a55f0cb84`. The integration merge is
`304f3008dabe431324cbc0d84b692207431e5126`. Development continues only on
`codex/sub2-v2911-cline-complete-20261007`; the old review, main, stable,
release tags, images, updater and production are not changed by this increment.

The merge retains atomic account creation while accepting upstream initial
quality plans, and preserves the previously accepted frontend dependency
security maintenance. No Ticket, cookie-jar, custom updater or patchpack is
restored. Do not install a pure upstream binary expecting it to retain this
source overlay.

## Corrected request contracts

`cline_auth_type=account_token` sends exactly one `workos:` prefix in the
Bearer value for inference and authenticated metadata requests. Raw and already
prefixed account tokens are accepted. API keys are not prefixed. Saved token
bytes and credential-bound CAS fingerprints are not rewritten by forwarding;
rotation invalidates old evidence. Control characters, duplicated prefix and
empty tokens fail without including credentials in diagnostics. This does not
implement OAuth login/refresh or fabricate session/organization identity.

All Cline Chat transport requests explicitly send the negotiated `stream` bool,
including `false` after Responses/Messages lowering and compaction. A false
request removes `stream_options`. Request `providerOptions`, large JSON numbers,
model restrictions, protected core fields and bounded cancellation remain.
When a forced tool has no supported lowered declaration, reject with 400 before
network I/O instead of silently replacing the caller's choice with auto.
Automatic and none retain their existing semantics; this does not implement
provider-native server tools.

The bounded account/request header allowlist additionally accepts `HTTP-Referer`
and `X-Title`. Request overrides are removed from model JSON and do not persist;
precedence remains defaults < account < request. Metadata requests do not inherit
these overrides. Authentication, tenant/routing and framing headers stay blocked.
Additive migration 270 updates the existing database guard function; historical
migration checksums and saved credentials are untouched.

## Quota threshold scheduling

`IncrementQuotaUsed` updates counters and inserts the scheduler outbox event in
one SQL statement using the caller's transaction client. A transition from below
to at/above any enabled total/daily/weekly threshold emits one account event,
including transitions in a newly reset rolling/fixed period. Multiple dimensions
crossing together do not duplicate it. Row locking serializes concurrent updates.
Outbox failure and outer transaction rollback roll back the counters too.
Zero/absent limits remain unlimited. This is not reservation-based budgeting:
already-admitted concurrent requests may settle past a local limit. Local quota
counters are not an estimate of Cline Pass subscription capacity.

## Opaque reasoning and observed provider

Raw Chat already preserves the upstream JSON/SSE payload. Converted Responses
assistant/tool output items and Messages text/tool blocks now carry an optional
`reasoning_details` vendor extension, including unknown JSON fields and exact
large integers. Signed/encrypted strings are not converted into fabricated
OpenAI `encrypted_content` or native Anthropic signatures. Subsequent supported
assistant/tool history forwards the extension back; orphan signed history fails
rather than silently dropping it during normalization.

Streaming accumulates bounded details even when they arrive after visible
content or tool deltas. Documented string fragments are joined only for the same
explicit type/index or id; conflicting metadata and excessive payloads fail
without a successful terminal. Responses returns the complete details on the
matching `output_item.done` and final output item. Messages exposes the complete
extension on terminal `message_delta.delta.reasoning_details`. Clients must retain
this vendor extension to round-trip it: unmodified SDK accumulators may ignore
unknown fields. This is **not** a claim of native Responses/WebSocket compatibility
or universal signed-thinking interoperability. Final tool item IDs stay stable
between added/done/final output events.

Converted replies optionally include `cline_provider` with
`source=upstream_response` and the safe, explicitly reported provider name.
Conflicting stream observations produce `conflict=true` without a chosen name.
Missing/invalid evidence remains unknown; requested providerOptions and model
names are not execution evidence. This is self-reported provenance, not independent
attestation. It does not alter billing, routing or model mapping, and is not a new
persisted provider-analytics database.

## Model capabilities

After successful identity metadata, a bounded optional unauthenticated GET to
`/api/v1/ai/cline/models` enriches the recommendation catalog by exact full model
ID. Only explicit context/output limits and image/tool/reasoning fields are shown.
Absent evidence remains unknown, distinct from explicit false. No model-name
suffix matching, guessed capabilities, inferred Pass capacity, implicit selection
or paid fallback is introduced. Catalog failure does not invalidate a valid
credential or clear quota evidence. Existing metadata polling and cooldown
bounds still apply; refresh never sends a billable inference probe.

## Primary protocol sources

Reviewed Cline source at `b2c7148cd9286317875d46046efa9fd06caf7288`:
- `apps/vscode/src/sdk/account-service.ts` (WorkOS Bearer routing)
- `apps/vscode/src/shared/messages/content.ts` (reasoning_details on text/tool blocks)
- `docs/api/chat-completions.mdx` and `docs/api/models.mdx` (reasoning and catalog)
- `sdk/packages/core/src/cloud/models.ts` (official catalog endpoints/envelopes)
- `sdk/packages/llms/src/catalog/catalog-cline-recommended.ts` (recommendation schema;
  its guessed defaults are deliberately not copied into this gateway).

## Validation and release gate

The Cline-only workflow validates the exact checked-out SHA, manifest boundary,
all shared bridge tests, required new auth/HTTP/capability regressions, race suites,
real PostgreSQL quota concurrency/outbox/rollback tests, all existing durable HTTP
billing scenarios and offline migration tests. Frontend checks include selection,
unknown/false capabilities, complete tests, lint, typecheck and build. Missing,
skipped or failing required tests are failures, not evidence of completion.

Only recorded final-SHA job/artifact results establish a passed gate; writing this
document does not establish that CI passed. All transports and credentials in
these tests are synthetic and PostgreSQL is disposable. Real provider credentials,
optional official metadata permissions, production NewAPI forwarding, SMTP and
live deployment have not been exercised. Keep the review Draft until the exact
final code is independently reviewed. Downgrading must follow normal database
backup/compatibility procedures; never edit old migration checksums.
