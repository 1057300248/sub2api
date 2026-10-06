#!/usr/bin/env python3
"""One-shot, pinned Cline-only transplant; used only on the isolated build branch."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

UPSTREAM = 'ab7cb27ceed6390441277784cc5560694d95fd73'
BASE = 'bc83ff9c367883e5b7d0140e6bb42e2e7cc5239c'
SOURCE = '116ab640aca8331ea74e12ce8773c2fc85463d54'
TARGET = 'codex/sub2-v2910-cline-only-20261006'
ROOT = Path.cwd()

def git(*args, cwd=ROOT, data=None, check=True):
    p = subprocess.run(['git', *args], cwd=cwd, input=data, stdout=subprocess.PIPE,
                       stderr=subprocess.PIPE, text=True)
    if check and p.returncode:
        raise RuntimeError(f'git {args}: {p.stdout}\n{p.stderr}')
    return p

SHARED = set('''Makefile
backend/ent/schema/user_platform_quota.go
backend/internal/handler/admin/account_data.go
backend/internal/handler/admin/account_handler.go
backend/internal/handler/admin/group_handler.go
backend/internal/handler/endpoint.go
backend/internal/handler/gateway_handler.go
backend/internal/handler/openai_chat_completions.go
backend/internal/handler/openai_gateway_handler.go
backend/internal/repository/account_create_atomic.go
backend/internal/repository/account_repo.go
backend/internal/repository/migrations_runner.go
backend/internal/repository/scheduler_cache.go
backend/internal/server/routes/admin.go
backend/internal/server/routes/gateway.go
backend/internal/service/account.go
backend/internal/service/account_test_service.go
backend/internal/service/admin_account.go
backend/internal/service/admin_group.go
backend/internal/service/channel_monitor_quota_fetcher.go
backend/internal/service/channel_monitor_validate.go
backend/internal/service/channel_service.go
backend/internal/service/channel_service_test.go
backend/internal/service/composite_platform.go
backend/internal/service/composite_platform_test.go
backend/internal/service/domain_constants.go
backend/internal/service/gateway_service.go
backend/internal/service/model_rate_limit.go
backend/internal/service/openai_codex_models_service.go
backend/internal/service/openai_gateway_cc_pipeline.go
backend/internal/service/openai_gateway_chat_completions_raw.go
backend/internal/service/openai_gateway_count_tokens.go
backend/internal/service/openai_gateway_forward.go
backend/internal/service/openai_gateway_messages_chat_fallback.go
backend/internal/service/openai_gateway_responses_chat_fallback.go
backend/internal/service/openai_gateway_scheduling.go
backend/internal/service/openai_messages_dispatch.go
backend/internal/service/ratelimit_service.go
backend/internal/service/scheduler_snapshot_service.go
backend/migrations/241_add_typesafe_platform.sql
backend/migrations/typesafe_platform_migration_test.go
frontend/src/components/common/PlatformIcon.vue
frontend/src/components/keys/UseKeyModal.vue
frontend/src/constants/platforms.ts
frontend/src/i18n/locales/en/admin/accounts.ts
frontend/src/i18n/locales/en/admin/overview.ts
frontend/src/i18n/locales/en/index.ts
frontend/src/i18n/locales/zh/admin/accounts.ts
frontend/src/i18n/locales/zh/admin/overview.ts
frontend/src/i18n/locales/zh/index.ts
frontend/src/types/index.ts
frontend/src/utils/keyGroupProviders.ts
frontend/src/utils/platformColors.ts
frontend/src/views/admin/AccountsView.vue
tools/verify_cline_platform.py'''.splitlines())

def cline_file(path):
    return path in SHARED or (path.startswith(('backend/', 'frontend/')) and
            ('cline' in Path(path).name.lower() or '/clinemigration/' in path or '/cline/' in path or '/cline-migrate/' in path))

changed = git('diff', '--name-only', BASE, SOURCE).stdout.splitlines()
selected = [p for p in changed if cline_file(p)]
print('PINNED_UPSTREAM', UPSTREAM, 'PINNED_CLINE_SOURCE', SOURCE, flush=True)
print('KEEP', json.dumps(selected, indent=2), flush=True)
print('DROP', json.dumps(sorted(set(changed) - set(selected)), indent=2), flush=True)
work = Path(tempfile.mkdtemp(prefix='cline-only-', dir=os.environ.get('RUNNER_TEMP')))
git('worktree', 'add', '--detach', str(work), UPSTREAM)
resolutions_path = ROOT / 'tools/cline_only_resolutions.json'
resolutions = json.loads(resolutions_path.read_text()) if resolutions_path.exists() else {}
unresolved = []

for path in selected:
    patch = git('diff', '--binary', '--full-index', BASE, SOURCE, '--', path).stdout
    sections = re.split(r'(?m)(?=^@@ )', patch)
    kept = [sections[0]]
    for hunk in sections[1:]:
        delta = '\n'.join(line[1:] for line in hunk.splitlines() if line.startswith(('+', '-')))
        if 'codex_cookie_jar' in delta:
            if 'cline' in delta.lower():
                raise RuntimeError(f'Mixed Cline/cookie hunk requires manual review: {path}\n{hunk}')
            print('REMOVED_LEGACY_COOKIE_HUNK', path, hunk, flush=True)
        else:
            kept.append(hunk)
    if len(sections) > 1 and len(kept) == 1:
        continue
    applied = git('apply', '--3way', '--index', '-', cwd=work, data=''.join(kept), check=False)
    if applied.returncode:
        print('APPLY_ERROR', path, applied.stdout, applied.stderr, flush=True)
        edits = resolutions.get(path)
        if edits:
            current = (work / path).read_text()
            for edit in edits:
                if current.count(edit['old']) != 1:
                    raise RuntimeError(f'Exact conflict resolution no longer matches: {path}')
                current = current.replace(edit['old'], edit['new'], 1)
            if re.search(r'(?m)^(<<<<<<<|=======|>>>>>>>)', current):
                raise RuntimeError(f'Incomplete reviewed resolution: {path}')
            (work / path).write_text(current)
            git('add', '--', path, cwd=work)
            print('RESOLVED_WITH_REVIEWED_EXACT_EDIT', path, flush=True)
        else:
            unresolved.append(path)
if unresolved:
    print('UNRESOLVED_PATHS', unresolved, flush=True)
    print(git('diff', '--cc', cwd=work, check=False).stdout, flush=True)
    raise RuntimeError('Unresolved transplant; nothing was pushed')

# This manifest describes only Cline. There is no automatic fork updater.
docdir = work / 'docs/cline'
docdir.mkdir(parents=True, exist_ok=True)
allowed = sorted(set(selected) | {'docs/cline/patches.json', 'docs/cline/UPGRADE_2.9.10.md',
                                 'tools/verify_cline_only.py', '.github/workflows/cline-only.yml'})
manifest = {'schema_version': 1, 'upstream_commit': UPSTREAM, 'upstream_tag': 'v2.9.10',
            'cline_source_commit': SOURCE,
            'modules': [{'id': name, 'mode': 'source-overlay', 'review_on_upstream_touch': True}
                        for name in ('cline-platform', 'cline-rate-limit-cas')],
            'allowed_changed_files': allowed}
(docdir / 'patches.json').write_text(json.dumps(manifest, indent=2) + '\n')
verifier = work / 'tools/verify_cline_platform.py'
text = verifier.read_text()
old = "ROOT/'.wanchuan/patches/manifest.json'"
assert text.count(old) == 1
verifier.write_text(text.replace(old, "ROOT/'docs/cline/patches.json'"))

(work / 'tools/verify_cline_only.py').write_text('''#!/usr/bin/env python3
"""Reject non-Cline deviations from the pinned upstream release (no auto-updater)."""
import json
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
manifest = json.loads((root / 'docs/cline/patches.json').read_text())
base = manifest['upstream_commit']
def git(*args):
    return subprocess.check_output(['git', *args], cwd=root, text=True)
changed = set(git('diff', '--name-only', base, '--').splitlines())
allowed = set(manifest['allowed_changed_files'])
assert not changed - allowed, f'Non-Cline deviations: {sorted(changed - allowed)}'
assert {m['id'] for m in manifest['modules']} == {'cline-platform', 'cline-rate-limit-cas'}
for path in ('backend/internal/config/config.go', 'backend/internal/service/openai_codex_ticket.go',
             'backend/internal/service/openai_codex_ticket_feedback.go', 'backend/internal/service/update_service.go',
             'backend/internal/service/update_service_test.go', 'backend/internal/repository/github_release_service.go',
             '.github/workflows/release.yml', 'frontend/package.json', 'frontend/pnpm-lock.yaml'):
    assert git('diff', base, '--', path) == '', f'Must remain upstream-owned: {path}'
for path in git('ls-files').splitlines():
    assert not path.startswith('.wanchuan/'), f'Retired patchpack: {path}'
    assert 'custom_patch_' not in path, f'Retired generic regression overlay: {path}'
    assert not path.startswith('.github/workflows/wanchuan-'), f'Retired fork workflow: {path}'
    if path.startswith(('backend/', 'frontend/')) and path in changed:
        patch = git('diff', '--unified=0', base, '--', path)
        additions = '\\n'.join(x[1:] for x in patch.splitlines() if x.startswith('+') and not x.startswith('+++'))
        for forbidden in ('codex_cookie_jar', 'wanchuan-release.json', 'WanchuanReleaseManifest'):
            assert forbidden not in additions, f'Retired non-Cline code in {path}: {forbidden}'
print(f'Cline-only boundary: PASS; {len(changed)} changed paths against {base}')
''')
(docdir / 'UPGRADE_2.9.10.md').write_text(f'''# Sub2API 2.9.10 + Cline only

- Upstream: `ranxi2001/sub2api@{UPSTREAM}` (`v2.9.10`).
- Cline source: `1057300248/sub2api@{SOURCE}`; original base `{BASE}`.
- Scope: only Cline platform/CAS and their necessary routing, schema, frontend and tests.
- A pristine upstream tree is used; shared-file patches are applied with Git three-way merging.

## Retained Cline contracts

Independent Cline accounts and legacy official-host OpenAI/DeepSeek compatibility;
Pass/payg/Free/unknown classification (Free and unknown public inference remain disabled);
explicit model mapping and no silent paid fallback; scoped quota/CAS and wrapped 429 handling;
request-level custom parameters through Chat, Responses and Messages (including compaction);
SSE/JSON error guards, bounded cancellation and earliest caller deadline;
observed-usage settlement and credential/metadata ownership; Cline-only administration and UI.
Historical Cline migrations 263-267 and the exact TypeSafe/Cline CHECK-union compatibility
remain necessary for upgrades. They are not replaced, renumbered or removed.

## Removed non-Cline customization

Ticket TTL/default overrides and legacy `codex_cookie_jar` privacy/write overlays;
Wanchuan release-source, revision, manifest and updater customizations;
Wanchuan sync/promote/scheduler/patchpack machinery; generic model/operations regression
wrappers, unrelated test changes and dependency overrides. Those implementations now
match the pinned upstream release, not the older fork.

## Operational consequences

The built-in updater now behaves exactly like upstream and downloads upstream releases.
It DOES NOT reapply Cline. Do not use it to update a deployment that must retain Cline;
deploy an explicitly built and tested Cline branch artifact instead. No replacement
custom updater or forced Ticket configuration is introduced by this branch.

Legacy `codex_cookie_jar` rows are not deleted by this code change. The custom protection
was removed as requested. Before production adoption, inspect and sanitize historical
rows and import/backup workflows using a separately reviewed data operation. Do not
expose an export that still contains obsolete credential material.

This branch operation does not migrate production, rebind accounts, publish a release,
change main/stable, call real providers, or incur inference charges.

## Verification

Run `python3 tools/verify_cline_only.py`, `python3 tools/verify_cline_platform.py`,
Cline unit/race suites, real PostgreSQL migration/ownership/HTTP ledger tests, and frontend
tests/build. CI results must be read for the exact final commit; this document does not
claim unexecuted checks passed. The Git history must include the pinned upstream object.
''')
git('add', '-A', cwd=work)
subprocess.run(['python3', 'tools/verify_cline_only.py'], cwd=work, check=True)
subprocess.run(['python3', 'tools/verify_cline_platform.py'], cwd=work, check=True)
git('diff', '--cached', '--check', cwd=work)
print('FINAL_STAT\n' + git('diff', '--cached', '--stat', cwd=work).stdout, flush=True)
print('SHARED_CLINE_DIFF\n' + git('diff', '--cached', '--', *sorted(SHARED - {'tools/verify_cline_platform.py'}), cwd=work).stdout, flush=True)
remote = git('ls-remote', 'origin', f'refs/heads/{TARGET}').stdout.split()
if not remote or remote[0] != UPSTREAM:
    raise RuntimeError('Target branch moved; refusing to overwrite concurrent work')
git('-c', 'user.name=github-actions[bot]', '-c', 'user.email=41898282+github-actions[bot]@users.noreply.github.com',
    'commit', '-m', 'feat(cline): transplant Cline-only integration onto upstream v2.9.10', cwd=work)
head = git('rev-parse', 'HEAD', cwd=work).stdout.strip()
git('push', 'origin', f'HEAD:refs/heads/{TARGET}', cwd=work)
print('PUSHED_CLINE_ONLY_HEAD=' + head, flush=True)
if os.environ.get('GITHUB_OUTPUT'):
    with open(os.environ['GITHUB_OUTPUT'], 'a') as f:
        f.write('head=' + head + '\n')
