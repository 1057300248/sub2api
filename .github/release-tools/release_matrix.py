#!/usr/bin/env python3
"""Split GoReleaser builds across runners without requiring GoReleaser Pro."""
import argparse
import hashlib
import itertools
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
from datetime import datetime, timezone

import yaml

FULL_CONFIG = Path('.goreleaser.yaml')
SIMPLE_CONFIG = Path('.goreleaser.simple.yaml')
VERSION_FILE = Path('backend/cmd/server/VERSION')
VERSION_RE = re.compile(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?')
WANCHUAN_RELEASE_RE = re.compile(r'\d+\.\d+\.\d+-wanchuan\.[1-9]\d*')
PATCH_MANIFEST_FILE = Path('.wanchuan/patches/manifest.json')
UPSTREAM_LOCK_FILE = Path('.wanchuan/upstream.lock')
WANCHUAN_RELEASE_MANIFEST = 'wanchuan-release.json'


def config(simple=False):
    return yaml.safe_load((SIMPLE_CONFIG if simple else FULL_CONFIG).read_text())


def is_wanchuan_release(version):
    return bool(WANCHUAN_RELEASE_RE.fullmatch(version))


def targets(simple=False):
    build = config()['builds'][0]
    result = []
    for goos, goarch in itertools.product(build['goos'], build['goarch']):
        item = {'goos': goos, 'goarch': goarch}
        if any(all(item.get(k) == v for k, v in rule.items()) for rule in build.get('ignore', [])):
            continue
        if not simple or item == {'goos': 'linux', 'goarch': 'amd64'}:
            result.append(item)
    if not result:
        raise ValueError('empty release target matrix')
    return result


def archive_name(version, target):
    if not VERSION_RE.fullmatch(version) or target not in targets():
        raise ValueError('invalid release version or target')
    suffix = 'zip' if target['goos'] == 'windows' else 'tar.gz'
    return f"sub2api_{version}_{target['goos']}_{target['goarch']}.{suffix}"


def sha256(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def build_wanchuan_release_manifest(version, source_sha):
    if not is_wanchuan_release(version):
        raise ValueError('release manifest requires a Wanchuan revision')
    if not re.fullmatch(r'[0-9a-f]{40}', source_sha):
        raise ValueError('release source sha must be a full lowercase commit sha')
    patch_manifest = json.loads(PATCH_MANIFEST_FILE.read_text())
    upstream_lock = json.loads(UPSTREAM_LOCK_FILE.read_text())
    for key in ('upstream_repo', 'release_repo'):
        if not isinstance(patch_manifest.get(key), str) or not patch_manifest[key]:
            raise ValueError(f'patch manifest missing {key}')
    if upstream_lock.get('upstream_repo') != patch_manifest['upstream_repo']:
        raise ValueError('upstream lock repository does not match patch manifest')
    for key in ('sha', 'integrated_commit'):
        if not re.fullmatch(r'[0-9a-f]{40}', str(upstream_lock.get(key, ''))):
            raise ValueError(f'upstream lock {key} must be a full lowercase commit sha')
    modules = sorted(
        module['id'] for module in patch_manifest.get('modules', [])
        if isinstance(module, dict) and isinstance(module.get('id'), str) and module['id']
    )
    if not modules:
        raise ValueError('patch manifest has no modules')
    return {
        'schema_version': 1,
        'channel': 'wanchuan',
        'version': version,
        'source_sha': source_sha,
        'release_repo': patch_manifest['release_repo'],
        'upstream_repo': patch_manifest['upstream_repo'],
        'upstream_tag': upstream_lock.get('tag'),
        'upstream_sha': upstream_lock['sha'],
        'integration_commit': upstream_lock['integrated_commit'],
        'patch_manifest_sha256': sha256(PATCH_MANIFEST_FILE),
        'patch_modules': modules,
    }


def write_release_manifest(args):
    manifest = build_wanchuan_release_manifest(args.version, args.sha)
    Path(args.output).write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')


def plan(args):
    sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    if args.dry_run:
        version = VERSION_FILE.read_text().strip()
        tag = 'v' + version
    else:
        tag = args.ref
        version = tag.removeprefix('v')
        if not tag.startswith('v') or not VERSION_RE.fullmatch(version):
            raise ValueError('publishing requires a v-prefixed release version tag')
        tagged_sha = subprocess.check_output(['git', 'rev-parse', '--verify', f'refs/tags/{tag}^{{commit}}'], text=True).strip()
        if sha != tagged_sha:
            raise ValueError('checkout does not match the selected release tag')
    if not VERSION_RE.fullmatch(version):
        raise ValueError('invalid VERSION')
    VERSION_FILE.write_text(version + '\n')
    fork_release = is_wanchuan_release(version)
    result = {'sha': sha, 'tag': tag, 'version': version,
              'prerelease': str('-' in version and not fork_release).lower(),
              'fork_release': str(fork_release).lower(),
              'owner_lower': os.environ.get('GITHUB_REPOSITORY_OWNER', '').lower(),
              'simple': str(args.simple).lower(), 'dry_run': str(args.dry_run).lower(),
              'date': datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'),
              'matrix': json.dumps({'include': targets(args.simple)}, separators=(',', ':'))}
    with Path(os.environ['GITHUB_OUTPUT']).open('a') as output:
        for key, value in result.items():
            output.write(f'{key}={value}\n')


def generate_config(args):
    data = config(args.simple if args.mode == 'publish' else False)
    data['snapshot'] = {'version_template': '{{ .Env.RELEASE_VERSION }}'}
    if args.mode == 'publish' and is_wanchuan_release(os.environ.get('RELEASE_VERSION', '')):
        # Wanchuan revisions are stable owner releases, not upstream prereleases.
        # Marking them stable keeps GitHub's /releases/latest endpoint compatible
        # with the built-in updater.
        data['release']['prerelease'] = False
        data['release']['make_latest'] = True
    data['dockers'] = []
    data['docker_manifests'] = []
    if args.mode == 'build':
        target = {'goos': args.goos, 'goarch': args.goarch}
        if target not in targets():
            raise ValueError('unsupported build target')
        for build in data['builds']:
            build['goos'], build['goarch'], build['ignore'] = [args.goos], [args.goarch], []
            build['ldflags'] = [re.sub(r'{{\s*\.Date\s*}}', '{{ .Env.RELEASE_DATE }}', flag)
                                for flag in build.get('ldflags', [])]
    else:
        # Artifacts are supplied through the OSS extra_files mechanism. No build
        # is repeated on the publishing runner, and release templates stay intact.
        data['before'] = {'hooks': []}
        data['builds'] = [{'id': 'sub2api', 'skip': True}]
        data['archives'] = []
        extra = [{'glob': 'release-input/sub2api_*.tar.gz'}, {'glob': 'release-input/sub2api_*.zip'}]
        if is_wanchuan_release(os.environ.get('RELEASE_VERSION', '')):
            extra.append({'glob': f'release-input/{WANCHUAN_RELEASE_MANIFEST}'})
        if args.simple:
            data['checksum'] = {'disable': True}
        else:
            data['release']['extra_files'] = extra
            data['checksum'] = {'name_template': 'checksums.txt', 'algorithm': 'sha256', 'extra_files': extra}
    Path(args.output).write_text(yaml.safe_dump(data, sort_keys=False, allow_unicode=True))


def collect(args):
    target = {'goos': args.goos, 'goarch': args.goarch}
    name = archive_name(args.version, target)
    source = Path('dist') / name
    checksums = {line.split()[1].lstrip('*'): line.split()[0] for line in Path('dist/checksums.txt').read_text().splitlines()}
    digest = sha256(source)
    if checksums.get(name) != digest:
        raise ValueError('archive does not match the build checksum')
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, output / name)
    manifest = {'sha': args.sha, 'version': args.version, 'target': target, 'archive': name, 'sha256': digest}
    (output / f"manifest-{args.goos}-{args.goarch}.json").write_text(json.dumps(manifest) + '\n')


def verify(args):
    directory = Path(args.input)
    expected = set()
    for target in targets(args.simple):
        name = archive_name(args.version, target)
        manifest_name = f"manifest-{target['goos']}-{target['goarch']}.json"
        expected.update((name, manifest_name))
        manifest = json.loads((directory / manifest_name).read_text())
        if manifest != {'sha': args.sha, 'version': args.version, 'target': target,
                        'archive': name, 'sha256': sha256(directory / name)}:
            raise ValueError(f'build provenance or checksum mismatch: {name}')
    if {p.name for p in directory.iterdir()} != expected:
        raise ValueError('missing or unexpected release artifacts')


def contexts(args):
    verify(args)
    for target in targets(args.simple):
        if target['goos'] != 'linux':
            continue
        dest = Path(args.output) / target['goarch']
        dest.mkdir(parents=True, exist_ok=True)
        with tarfile.open(Path(args.input) / archive_name(args.version, target), 'r:gz') as archive:
            members = [member for member in archive.getmembers() if member.name in ('sub2api', './sub2api')]
            if len(members) != 1 or not members[0].isfile():
                raise ValueError('archive must contain one regular sub2api binary')
            with archive.extractfile(members[0]) as source, (dest / 'sub2api').open('wb') as output:
                shutil.copyfileobj(source, output)
        (dest / 'sub2api').chmod(0o755)
        shutil.copy2('Dockerfile.goreleaser', dest / 'Dockerfile')
        (dest / 'deploy').mkdir(exist_ok=True)
        shutil.copy2('deploy/docker-entrypoint.sh', dest / 'deploy/docker-entrypoint.sh')
        shutil.copytree('backend/resources', dest / 'backend/resources', dirs_exist_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    p = commands.add_parser('plan')
    p.add_argument('--ref', required=True)
    p.add_argument('--simple', action='store_true')
    p.add_argument('--dry-run', action='store_true')
    p.set_defaults(run=plan)
    p = commands.add_parser('config')
    p.add_argument('mode', choices=['build', 'publish'])
    p.add_argument('--goos')
    p.add_argument('--goarch')
    p.add_argument('--simple', action='store_true')
    p.add_argument('--output', required=True)
    p.set_defaults(run=generate_config)
    p = commands.add_parser('collect')
    for arg in ('version', 'sha', 'goos', 'goarch', 'output'):
        p.add_argument('--' + arg, required=True)
    p.set_defaults(run=collect)
    p = commands.add_parser('manifest')
    for arg in ('version', 'sha', 'output'):
        p.add_argument('--' + arg, required=True)
    p.set_defaults(run=write_release_manifest)
    for command, handler in [('verify', verify), ('contexts', contexts)]:
        p = commands.add_parser(command)
        for arg in ('version', 'sha', 'input'):
            p.add_argument('--' + arg, required=True)
        p.add_argument('--simple', action='store_true')
        if command == 'contexts':
            p.add_argument('--output', required=True)
        p.set_defaults(run=handler)
    args = parser.parse_args()
    args.run(args)


if __name__ == '__main__':
    main()
