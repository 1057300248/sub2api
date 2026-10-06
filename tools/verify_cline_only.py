#!/usr/bin/env python3
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
maintenance = set(manifest.get('maintenance_changed_files', []))
assert maintenance <= allowed, f'Maintenance files must be allowed: {sorted(maintenance - allowed)}'
assert not changed - allowed, f'Non-Cline deviations: {sorted(changed - allowed)}'
assert {m['id'] for m in manifest['modules']} == {'cline-platform', 'cline-rate-limit-cas'}
for path in ('backend/internal/config/config.go', 'backend/internal/service/openai_codex_ticket.go',
             'backend/internal/service/openai_codex_ticket_feedback.go', 'backend/internal/service/update_service.go',
             'backend/internal/service/update_service_test.go', 'backend/internal/repository/github_release_service.go',
             '.github/workflows/release.yml', 'frontend/package.json', 'frontend/pnpm-lock.yaml'):
    if path in maintenance:
        continue
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
print(f'Cline-only boundary: PASS; {len(changed)} changed paths against {base}')
