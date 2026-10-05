#!/usr/bin/env python3
"""Validate the package's embedded runtime, trust store and complete CRD payload."""
import hashlib
import json
import pathlib
import re
import tarfile

root = pathlib.Path(__file__).resolve().parents[1]
work = root / '.work'
expected_binary = hashlib.sha256((work / 'bin/provider').read_bytes()).hexdigest()
with tarfile.open(work / 'provider-upjet-logto.xpkg') as package:
    manifests = json.load(package.extractfile('manifest.json'))
    assert len(manifests) == 1, 'Expected one package manifest'
    manifest = manifests[0]
    config = json.load(package.extractfile(manifest['Config']))
    assert config['config']['User'] == '65532:65532', 'Runtime must be non-root'
    assert config['config']['Entrypoint'] == ['/usr/local/bin/provider'], 'Wrong entrypoint'
    payload = None
    has_binary = has_ca = False
    for layer_name in manifest['Layers']:
        with tarfile.open(fileobj=package.extractfile(layer_name)) as layer:
            for member in layer:
                name = member.name.lstrip('./')
                if name == 'usr/local/bin/provider':
                    actual = hashlib.sha256(layer.extractfile(member).read()).hexdigest()
                    assert actual == expected_binary, 'Package embeds a different executable'
                    assert member.mode & 0o111, 'Provider is not executable'
                    has_binary = True
                if name == 'etc/ssl/certs/ca-certificates.crt':
                    assert member.size > 0, 'Empty trust store'
                    has_ca = True
                if name == 'package.yaml':
                    assert payload is None, 'Multiple package manifests'
                    payload = layer.extractfile(member).read().decode()
    assert has_binary and has_ca, 'Missing executable or HTTPS trust store'
    expected_crds = list((root / 'package/crds').glob('*.yaml'))
    assert payload is not None, 'Missing Crossplane package payload'
    assert payload.count('kind: CustomResourceDefinition\n') == len(expected_crds) == 27, 'Incomplete CRD bundle'
    assert payload.count('kind: Provider\n') == 1, 'Missing provider metadata'
    for crd in expected_crds:
        name = re.search(r'^  name: (\S+)$', crd.read_text(), re.MULTILINE)[1]
        assert f'  name: {name}\n' in payload, f'Missing CRD {name}'
sbom = json.loads((work / 'runtime.spdx.json').read_text())
assert sbom['spdxVersion'].startswith('SPDX-2.'), 'Invalid SPDX SBOM'
assert sbom.get('packages'), 'SBOM contains no software inventory'
checksums = []
for name in ['provider-upjet-logto.xpkg', 'runtime.spdx.json']:
    checksums.append(f"{hashlib.sha256((work / name).read_bytes()).hexdigest()}  {name}")
(work / 'SHA256SUMS').write_text('\n'.join(checksums) + '\n')
print('Verified non-root runtime, executable hash, HTTPS trust store, all 27 CRDs and SPDX SBOM')
