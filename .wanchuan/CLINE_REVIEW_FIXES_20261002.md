# Cline review-fix branch

Base: `1351636cce541b5206c2ac40b81bb7d56f8ab669`. Branch: `codex/cline-review-fixes-20261002`.

This change normalizes standard and explicit success/data non-stream completions before any of the three protocol bridges. Malformed/unsuccessful envelopes fail rather than generating empty success. Raw usage, tool and reasoning fields are retained; streaming requests which unexpectedly return JSON still fail.

Account creation and the scheduler outbox now share a transaction. The grouped repository capability also includes exact bindings; Cline administrator creation uses it. Existing outer transactions are reused, no uncommitted cache snapshot is published and an owned-transaction failure does not expose a generated account ID. Other platforms retain their service-level group-binding policy.

Cline upstream requests have a 30-minute absolute lifetime ceiling. Client cancellation or raw-stream write failure starts a non-renewable 30-second drain grace. The earlier deadline wins. Context cancellation interrupts the net/http request and body reading, and response Close owns timer cleanup. These limits apply to Cline only and do not extend a downstream/NewAPI timeout, lower first-token latency or promise recovery of usage that arrives after the deadline. Existing transport limits can still expire earlier. Evaluate actual client/proxy/header/idle/total timeouts with request-level production evidence before changing those settings; no NewAPI or production settings were changed here.

Account Token is a manually supplied, potentially expiring credential. No OAuth refresh-token protocol, automatic renewal, paid fallback or extra model entitlement is implemented. After a 401 the operator replaces the credential and explicitly refreshes metadata. Its lifetime is not hardcoded from a third-party issue report.

The alleged 332-versus-300 Free scope overflow is not a schema defect: the 332-byte fingerprinted key lives in JSON Extra; the SQL shared scope removes the fingerprint and is at most 267 bytes. A PostgreSQL regression exercises both write paths. Free inference remains disabled and no schema limit was widened.

New tests cover standard/wrapped non-stream forwarding for Chat/Responses/Messages with requested/upstream price selection, invalid envelopes, loopback HTTP drain cancellation, cleanup races, real PostgreSQL outbox rollback and outer transactions. The monetary test invokes actual forwarding then RecordUsage with a synthetic ledger; it is not a full HTTP-handler/production billing audit. No live key, paid inference, production migration, deployment, main/stable update or local test pass is claimed. Read exact-SHA CI results before marking this candidate accepted.
