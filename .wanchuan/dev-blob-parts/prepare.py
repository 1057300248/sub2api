"""Temporary development transport: prepare immutable reviewed blobs only.
Never execute candidates, edit the checkout, create commits, or move refs.
"""
import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import time
import urllib.error
import urllib.request
import zlib

REPO = '1057300248/sub2api'
REF = 'refs/heads/codex/cline-platform-modular-20261001'
DELTA_SHA256 = 'de90ecd3838b6cd27a3929eae441b982fbd4e9ae9d02676d7093d57c0992ce6f'


def blob_sha(body):
    return hashlib.sha1(b'blob ' + str(len(body)).encode() + b'\0' + body).hexdigest()


def candidates(payload):
    if hashlib.sha256(payload).hexdigest() != DELTA_SHA256:
        raise ValueError('reviewed delta digest mismatch')
    data = json.loads(payload)
    if data.get('schema') != 1 or len(data['files']) != 58:
        raise ValueError('unexpected reviewed delta schema/count')
    seen, result = set(), []
    for item in data['files']:
        name = item['p']
        path = PurePosixPath(name)
        if name in seen or path.is_absolute() or '..' in path.parts or '\\' in name or not re.fullmatch(r'[a-zA-Z0-9_./-]+', name):
            raise ValueError('invalid or duplicate candidate path')
        if name != 'Makefile' and path.suffix not in {'.go', '.py', '.json', '.md', '.yml', '.vue', '.ts'}:
            raise ValueError('unexpected candidate extension')
        seen.add(name)
        old = subprocess.run(['git', 'show', 'HEAD:' + name], capture_output=True, check=False)
        if item['old'] is None:
            if old.returncode == 0:
                raise ValueError('new candidate already exists: ' + name)
            before = b''
        else:
            if old.returncode or blob_sha(old.stdout) != item['old']:
                raise ValueError('candidate baseline changed: ' + name)
            before = old.stdout
        lines = before.decode('utf-8').splitlines(keepends=True)
        cursor, pieces = 0, []
        for start, end, replacement in item['edits']:
            if type(start) is not int or type(end) is not int or not cursor <= start <= end <= len(lines) or not isinstance(replacement, str):
                raise ValueError('invalid edit range: ' + name)
            pieces.extend(lines[cursor:start])
            pieces.append(replacement)
            cursor = end
        pieces.extend(lines[cursor:])
        body = ''.join(pieces).encode('utf-8')
        if len(body) > 2_000_000 or blob_sha(body) != item['new']:
            raise ValueError('candidate content mismatch: ' + name)
        result.append((name, item['old'], item['new'], body))
    return result


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('redirect prohibited')


def main():
    if os.environ.get('GITHUB_REPOSITORY') != REPO or os.environ.get('GITHUB_REF') != REF:
        raise ValueError('unexpected repository or ref')
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    if head != os.environ['GITHUB_SHA']:
        raise ValueError('checkout differs from event SHA')
    packed = b''.join(Path(f'.wanchuan/dev-blob-parts/{n}.bin').read_bytes() for n in range(4))
    decoder = zlib.decompressobj()
    payload = decoder.decompress(packed, 200_001)
    if len(payload) > 200_000 or not decoder.eof or decoder.unused_data or decoder.unconsumed_tail:
        raise ValueError('invalid or oversized reviewed delta')
    checked = candidates(payload)
    opener = urllib.request.build_opener(NoRedirect())
    receipts = []
    for name, before, expected, body in checked:
        request = urllib.request.Request(
            'https://api.github.com/repos/' + REPO + '/git/blobs',
            data=json.dumps({'content': base64.b64encode(body).decode(), 'encoding': 'base64'}).encode(),
            headers={'Authorization': 'Bearer ' + os.environ['GH_TOKEN'], 'Accept': 'application/vnd.github+json', 'Content-Type': 'application/json', 'X-GitHub-Api-Version': '2022-11-28'},
            method='POST')
        with opener.open(request, timeout=30) as response:
            saved = json.load(response)
        if saved.get('sha') != expected:
            raise ValueError('GitHub blob verification failed: ' + name)
        receipts.append({'path': name, 'old': before, 'sha': expected})
        print(name + ' ' + expected, flush=True)
        time.sleep(0.15)
    subprocess.run(['git', 'diff', '--exit-code'], check=True)
    out = Path(os.environ['RUNNER_TEMP']) / 'cline-reviewed-blobs.json'
    out.write_text(json.dumps({'event_sha': head, 'delta_sha256': DELTA_SHA256, 'files': receipts}, indent=2) + '\n')
    print('Prepared 58 immutable blobs. No candidate execution, checkout edits, commits, ref movement or validation claim.')


if __name__ == '__main__':
    try:
        main()
    except urllib.error.HTTPError as error:
        raise SystemExit('GitHub blob write failed with HTTP ' + str(error.code)) from None
