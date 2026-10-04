#!/usr/bin/env python3
"""Build a local Docker-compatible OCI runtime archive without a container daemon."""
import argparse
import hashlib
import io
import json
import pathlib
import re
import struct
import subprocess
import tarfile


def add_file(archive, name, data, mode=0o644):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = mode
    info.mtime = 0
    archive.addfile(info, io.BytesIO(data))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[1]
    work = root / '.work'
    work.mkdir(exist_ok=True)
    dockerfile = (root / 'cluster/images/provider-upjet-logto/Dockerfile').read_text()
    base = re.search(r'^FROM (\S+@sha256:[a-f0-9]{64})$', dockerfile, re.MULTILINE)
    if base is None:
        raise SystemExit('Runtime base image must be pinned by digest')
    base_cache = work / f'base-{args.arch}-{base[1].split(":")[-1]}.tar'
    if not base_cache.exists():
        partial = base_cache.with_suffix('.partial')
        subprocess.run(['crane', 'pull', '--platform', f'linux/{args.arch}', base[1], str(partial)], check=True)
        partial.replace(base_cache)
    binary = work / 'bin/provider'
    binary_data = binary.read_bytes()
    expected_machine = {'amd64': 62, 'arm64': 183}[args.arch]
    if binary_data[:4] != b'\x7fELF' or struct.unpack('<H', binary_data[18:20])[0] != expected_machine:
        raise SystemExit('Provider executable does not match the requested Linux architecture')
    layer_buffer = io.BytesIO()
    with tarfile.open(fileobj=layer_buffer, mode='w') as layer:
        add_file(layer, 'usr/local/bin/provider', binary_data, 0o755)
    layer_data = layer_buffer.getvalue()
    with tarfile.open(base_cache) as source:
        manifest = json.load(source.extractfile('manifest.json'))[0]
        config = json.load(source.extractfile(manifest['Config']))
        if config['architecture'] != args.arch or config['os'] != 'linux':
            raise SystemExit('Base platform mismatch')
        config['created'] = '1970-01-01T00:00:00Z'
        config['config'].update({
            'Entrypoint': ['/usr/local/bin/provider'], 'Cmd': [], 'User': '65532:65532',
            'ExposedPorts': {'8080/tcp': {}, '8081/tcp': {}},
        })
        env = {value.split('=', 1)[0]: value.split('=', 1)[1] for value in config['config'].get('Env', [])}
        env.update(TERRAFORM_PROVIDER_SOURCE='Lenstra/logto', TERRAFORM_PROVIDER_VERSION='0.0.15')
        config['config']['Env'] = [f'{key}={value}' for key, value in sorted(env.items())]
        config['rootfs']['diff_ids'].append('sha256:' + hashlib.sha256(layer_data).hexdigest())
        config.setdefault('history', []).append({'created': config['created'], 'created_by': 'COPY provider /usr/local/bin/provider'})
        config_data = json.dumps(config, sort_keys=True, separators=(',', ':')).encode()
        config_name = hashlib.sha256(config_data).hexdigest() + '.json'
        output = work / 'runtime.tar'
        with tarfile.open(output.with_suffix('.partial'), 'w') as target:
            for name in manifest['Layers']:
                member = source.getmember(name)
                target.addfile(member, source.extractfile(member))
            add_file(target, 'provider/layer.tar', layer_data)
            add_file(target, config_name, config_data)
            add_file(target, 'manifest.json', json.dumps([{
                'Config': config_name, 'Layers': manifest['Layers'] + ['provider/layer.tar'],
                'RepoTags': ['provider-upjet-logto:dev'],
            }], sort_keys=True).encode())
        output.with_suffix('.partial').replace(output)
    print(f'Built local {args.arch} runtime archive: {output}')


if __name__ == '__main__':
    main()
