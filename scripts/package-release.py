#!/usr/bin/env python3
"""Build release archives and a matching source-built Homebrew formula."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import subprocess
import tarfile
import urllib.request
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument('version')
parser.add_argument('--formula-only', action='store_true')
args = parser.parse_args()
version = args.version.removeprefix('v')
if not re.fullmatch(r'\d+\.\d+\.\d+', version):
    parser.error('expected a stable semantic version, e.g. v0.1.0')
dist = Path('dist')
dist.mkdir(exist_ok=True)
if not args.formula_only:
    archives = []
    for system in ('darwin', 'linux', 'windows'):
        for arch in ('amd64', 'arm64'):
            name = 'orkestar.exe' if system == 'windows' else 'orkestar'
            target = dist / f'{system}-{arch}' / name
            target.parent.mkdir(exist_ok=True)
            subprocess.run(['go', 'build', '-trimpath', '-ldflags',
                            f'-s -w -X main.version={version}', '-o', str(target),
                            './cmd/orkestar'], check=True,
                           env={**os.environ, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0'})
            suffix = 'zip' if system == 'windows' else 'tar.gz'
            archive = dist / f'orkestar-{system}-{arch}.{suffix}'
            files = [(target, name), (Path('LICENSE'), 'LICENSE'), (Path('README.md'), 'README.md')]
            if system == 'windows':
                with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as out:
                    for source, dest in files:
                        out.write(source, dest)
            else:
                with tarfile.open(archive, 'w:gz') as out:
                    for source, dest in files:
                        out.add(source, arcname=dest)
            archives.append(archive)
    (dist / 'SHA256SUMS').write_text(''.join(
        f'{hashlib.sha256(a.read_bytes()).hexdigest()}  {a.name}\n' for a in archives))
else:
    url = f'https://github.com/martintrifunov/orkestar/archive/refs/tags/v{version}.tar.gz'
    with urllib.request.urlopen(url, timeout=60) as response:
        digest = hashlib.sha256(response.read()).hexdigest()
    template = Path('packaging/homebrew/orkestar.rb.in').read_text()
    (dist / 'orkestar.rb').write_text(template.replace('@VERSION@', version).replace('@SHA256@', digest))
