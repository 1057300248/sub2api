#!/usr/bin/env python3
"""Guard the reviewed Cline/incident overlay against the pinned v2.10.0 source."""
import json
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
manifest = json.loads((root / 'docs/cline/patches.json').read_text())
base = manifest['upstream_commit']
assert base == '5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549'
assert manifest['upstream_tag'] == 'v2.10.0'
def git(*args):
    return subprocess.check_output(['git', *args], cwd=root, text=True)
changed = set(git('diff', '--name-only', base, '--').splitlines())
allowed = set(manifest['allowed_changed_files'])
assert changed == allowed, f'Overlay drift: extra={sorted(changed-allowed)}, absent={sorted(allowed-changed)}'
assert not manifest['maintenance_changed_files'], 'Do not downgrade or repin upstream dependencies'
assert {m['id'] for m in manifest['modules']} == {'cline-platform', 'cline-rate-limit-cas', 'cline-incident-diagnostics'}
for path in ('backend/go.mod', 'backend/go.sum', 'backend/cmd/server/VERSION',
             'frontend/package.json', 'frontend/pnpm-lock.yaml',
             'backend/internal/config/config.go', 'backend/internal/service/openai_codex_ticket.go',
             'backend/internal/service/openai_codex_ticket_feedback.go', 'backend/internal/service/update_service.go',
             'backend/internal/service/update_service_test.go', 'backend/internal/repository/github_release_service.go',
             'backend/internal/service/openai_ws_sse_acceleration.go',
             'backend/internal/service/openai_ws_forwarder_v2.go',
             'backend/internal/payment/provider/easypay.go',
             '.github/workflows/release.yml', '.github/workflows/backend-ci.yml'):
    assert git('diff', base, '--', path) == '', f'Must remain upstream-owned: {path}'
for path in git('ls-files').splitlines():
    assert not path.startswith('.wanchuan/'), f'Retired patchpack: {path}'
    assert 'custom_patch_' not in path, f'Retired generic regression overlay: {path}'
    assert not path.startswith('.github/workflows/wanchuan-'), f'Retired fork workflow: {path}'
    if path.startswith(('backend/', 'frontend/')) and path in changed:
        patch = git('diff', '--unified=0', base, '--', path)
        additions = '\n'.join(x[1:] for x in patch.splitlines() if x.startswith('+') and not x.startswith('+++'))
        for forbidden in ('codex_cookie_jar', 'wanchuan-release.json', 'WanchuanReleaseManifest'):
            assert forbidden not in additions, f'Retired non-Cline code in {path}: {forbidden}'
print(f'Cline/incident boundary: PASS; {len(changed)} paths against {base}')
