# Cline account settings parity audit

## Scope

Audited against the account flows in the pinned v2.9.10 Cline branch, starting
at 89241a2b823810791a879e793405d235fe68b490. This is a source/UI and synthetic
transport audit, not a production browser session or a provider entitlement test.
No NewAPI, main/stable, deployed database, release or production image is changed.

## Findings and resolution

| Surface | Finding / delivered behavior |
| --- | --- |
| Normal Add account | Cline existed only as a separate toolbar action. The normal platform chooser now opens the same native Cline editor with a back action. Only name/notes carry from the generic form, never another platform's credentials or model mapping. |
| Identity and connection | Explicit Pass/Free/PAYG/unknown and API-key/account-token kind, HTTPS base URL, no implicit platform conversion. Keys remain empty in edit DOM; omitting a replacement retains the saved key server-side. |
| Model and group settings | Explicit public/upstream mapping; native/composite groups according to mode. Existing missing groups and group model restrictions survive normal edits. Per-group model restrictions are editable on existing accounts like upstream; an empty restricted selection cannot silently become unrestricted. |
| Proxy | Supports original configured proxy, missing/inactive saved proxies, temporary fallback-origin preservation, and explicit zero sentinel to clear. |
| Scheduling | Concurrency, priority, load factor, active/inactive/error, schedulable, expiry at second precision, auto-pause. Native create/update HTTP handlers now carry schedulable=false instead of ignoring it. Free/unknown remain nonschedulable and admission guards still apply. |
| Multipliers | Separate account rate_multiplier, group_rate_multiplier and extra.cost_multiplier. Zero is preserved; negative/nonfinite values are rejected. The cost setting sends only the changed cost key, not quota/runtime state. These configure existing upstream fields, not a new billing formula. |
| Edit preservation | Native credential submissions are server-side deltas, not replacement of ordinary unedited options. Native Extra deltas retain omitted ordinary settings; repository-owned quota state still comes from the transaction-protected current row. Notes can be cleared with an empty string. Nullable proxy/load/expiry clear with zero, not an ignored null. |
| Default request headers | Optional saved defaults use the same positive list as request-level overrides. Request overrides take precedence; case aliases are not duplicated. Validation also covers mixed bulk and repository updates. Other platform header semantics are unchanged. No saved providerOptions/body overrides were added. |
| Bulk editing | Cline API-key accounts are recognized by the shared header editor. A mixed Cline/non-Cline selection cannot bypass the stricter Cline header checks. Existing common bulk fields remain available. |
| Account list | Native saved quota projection is rendered instead of an unrelated generic API-key quota view. Missing/stale/expired/invalid readings are unknown, not zero. Durable pending-recovery state is separate. Each row performs no provider or metadata API request. Projection is built before credential redaction, excludes catalog and raw identity/fingerprint state, and does not mutate source Extra. |
| Refresh during editing | Opening either Cline entry pauses account-list auto-refresh; closing resumes it. Metadata panel reads saved account mode, not an unsaved draft mode. |
| Connection test | Native Cline test models are filtered against saved mapping and mode; unavailable modes are disabled. Opening the test modal does not infer. Explicit text test uses the existing guarded backend path and at most 64 output tokens, and may consume quota. |
| Import/export and backend writes | Existing Cline import validation and managed-state stripping remain. New credential header validation also applies to direct/update/bulk repository writes. No data import/export or live test was executed in this change. |
| Platform, groups and selectors | Existing native platform constants, API-key/group/composite registration, list filtering and group bindings remain covered by retained registration/routing tests. Cline is not classified as DeepSeek. |

## Intentional capability differences

Do not copy unrelated provider controls merely to make forms look identical.
Cline has no implemented automatic OAuth login/refresh workflow; account tokens
are explicit manual credentials. Native upstream Responses/Messages, paid
fallback, unverified Free API access and same-account pool retry remain disabled.
The gateway still translates supported client protocols to Cline Chat. BPS,
provider-specific privacy/Ticket and image connection-test controls are not Cline
features. Legacy OpenAI/DeepSeek accounts pointing to the official host remain
legacy accounts; opening the native form never converts them.

A quota refresh requires a supported official metadata endpoint and authorized
credentials. Unknown or expired saved observations never prove recovery. UI
multipliers are administrator inputs, not evidence of the provider's actual cost.

## Required verification

The Cline workflow now includes the normal create modal, dedicated modal, bulk
editor, account list, saved usage cell and connection-test component tests, plus
pure settings tests. General critical frontend tests retain other-provider cases.
Backend tests cover the real HTTP bind/handler, native service delta semantics,
false/zero values, three-protocol header precedence and actual PostgreSQL
round-trip/ownership/credential guards. DTO tests assert credential-bound safe
quota projection. All synthetic transports/credentials are local fixtures.

Read CI artifacts for the final commit; this document is not a passing test log.
A deployed site will only gain the entry after deploying this branch's matching
frontend and backend; this task does not perform that deployment.
