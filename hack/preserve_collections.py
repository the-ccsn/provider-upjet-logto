#!/usr/bin/env python3
"""Retain explicit [] in optional collections while omitting nil values.

Go's omitzero preserves the distinction in Kubernetes JSON. The Terraform
jsoniter encoder does not support omitzero, so its tag intentionally emits
null for nil and [] for non-nil empty collections. Both map correctly to the
Framework's optional/computed collection semantics.
"""
from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
fields = {'redirect_uris', 'post_logout_redirect_uris', 'cors_allowed_origins', 'scope_ids', 'role_ids'}
changed = 0
for path in sorted((root / 'apis').rglob('zz_*_types.go')):
    lines = path.read_text().splitlines(keepends=True)
    output = []
    in_parameters = False
    in_init = False
    for line in lines:
        if line.startswith('type '):
            in_parameters = bool(re.match(r'type \w*(?:InitParameters|Parameters) struct \{', line))
            in_init = 'InitParameters struct {' in line
        if line.startswith('}'):
            in_parameters = False
        # Upjet emits required sensitive selectors as value structs in
        # initProvider too. An unset selector serializes as an empty reference,
        # which its secret resolver reads before the valid forProvider selector.
        if in_parameters and in_init and 'ConfigurationSecretRef v2.LocalSecretKeySelector' in line:
            output.append('\t// +kubebuilder:validation:Optional\n')
            line = line.replace('ConfigurationSecretRef v2.LocalSecretKeySelector', 'ConfigurationSecretRef *v2.LocalSecretKeySelector').replace('json:"configurationSecretRef"', 'json:"configurationSecretRef,omitempty"')
        tag = re.search(r'json:"([^" ,]+),omitempty" tf:"([^" ,]+),omitempty"', line)
        if in_parameters and tag and tag[2] in fields:
            line = line.replace(tag[0], f'json:"{tag[1]},omitzero" tf:"{tag[2]}"')
            if in_init:
                output.append('\t// +kubebuilder:validation:Optional\n')
            changed += 1
        output.append(line)
    text = ''.join(output)
    if text != path.read_text():
        path.write_text(text)
print(f'Preserved nil versus empty semantics for {changed} generated collection fields')
