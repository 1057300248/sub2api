# Cline protocol, model and provider evidence contract

## Opaque reasoning history

Raw Chat preserves provider JSON reasoning_details in messages/deltas. Responses and Messages bridges use `cline:v1:` versioned, AES-256-GCM gateway envelopes: Responses `reasoning.encrypted_content` and Messages `redacted_thinking.data`. These values are NOT native OpenAI/Anthropic ciphertext and are replayable only through this gateway's Cline path.

The original provider JSON block content is retained, with indexed/id-based stream fragments accumulated under 1 MiB/128-block bounds. Envelopes expire after24h and require a configured JWT signing secret of at least32 bytes; a domain-separated derived key is used, never the secret directly. Changing that secret invalidates existing envelopes. Authenticated associated data binds Sub2API user/API-key ID, upstream origin, mode, verified subject (or credential identity), and actual upstream model. With an unverified subject, credential rotation invalidates the envelope; verified same-subject keys can replay it. This does not infer identities behind a shared NewAPI-to-Sub2API key.

Replay matches the original assistant turn by the exact ordered tool-call tuples (ID, type, function name and arguments), or by an unambiguous content digest when no tools were called. Foreign, tampered, expired, mismatched or ambiguous history returns400, not silent reasoning loss. A provider result whose details cannot be safely preserved fails conversion while retaining actual usage for settlement; it is not emitted as synthetic success. No plaintext/provider opaque blocks are logged or cached by this feature.

Streaming emits added/done item or start/stop block events before a successful/incomplete terminal snapshot. Failed/truncated streams never gain synthetic success or replay artifacts. Compaction consumes valid incoming history but its output remains the existing compressed-summary contract, not an assertion that all provider-native state survives compaction. Cross-model history requires a fresh/compacted conversation rather than accepting foreign signed state.

## Model capability metadata

Enrichment uses only the official fixed public `/api/v1/ai/cline/models` endpoint (no bearer header, redirects or arbitrary origins), bounded at4MiB/4096 rows and refreshed with the existing bounded metadata flow. Recommended/Pass/Free buckets remain independently validated. `clineCloud` is ignored until separately designed and authorized.

Only explicit fields are retained: context/max output, supported input modalities, tools, reasoning, prompt-cache and supported effort values when supplied. False is different from absent/unknown; no capabilities or billing/entitlement are inferred from names/descriptions/prices. IDs must match exactly, including `cline-pass/`. Unavailable enrichment does not invalidate a usable credential or revoke a model. A last successful response is historical evidence, not a guarantee of future entitlement or comprehensive tool compatibility.

## Provider evidence

Existing top-level request `providerOptions.gateway.only` is passed through unchanged. Observations accept only structured `provider`, `provider_metadata.gateway.routing.finalProvider` or `providerMetadata.gateway.routing.finalProvider`. Missing metadata is unknown; conflicting reports remain conflicting. No data are inferred from generated content. Native account details show the latest bounded credential-bound observation, requested constraint, actual reported provider, source, upstream model, completion flag and timestamp; legacy official-host paths log equivalent bounded evidence without native account enrollment.

The observation wrapper does not modify response bytes, usage or retry behavior and delegates validated SSE completion to the existing guard. Early close/errors are not success; evidence buffers cap at1MiB, larger frames leave unknown. A failed attempt may replace the previous last observation, so the UI explicitly labels completion. Managed account edits/imports cannot overwrite the observation, and stale credential completions cannot attribute old provider data to a replacement key.

Provider mismatch is observable, not automatically fixed. In particular it does not retry already-generated output, change the model, bypass a Pass constraint or enable PAYG. A real permitted end-to-end staging test is still required to establish whether the current upstream enforces a given provider constraint.

## Sources checked for this iteration

- Cline API: https://docs.cline.bot/api/chat-completions (stream default and optional application headers).
- Cline models: https://docs.cline.bot/api/models (reasoning details and explicit capabilities).
- Official auth and catalog implementations: cline/cline SDK provider-auth-registry.ts and cloud/models.ts; source snapshots noted in the review discussion.
- https://github.com/cline/cline/issues/13821 is a community report about refresh rotation, not a guarantee of the exact token lifetime or authority to manage the CLI's refresh tokens.
- ranxi2001/sub2api v2.9.11 pinned in patches.json.

Tests use synthetic keys/transports and disposable databases. No real upstream model/cost/provider claim is established by those fixtures.
