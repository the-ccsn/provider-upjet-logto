#!/usr/bin/env python3
"""Preserve creation-only consent references during resolution and late init."""
from pathlib import Path
import subprocess


root = Path(__file__).resolve().parents[1]
method_marker = 'func (mg *UserConsentScopes) ResolveReferences('
guard_marker = '// Resolve initial parent references only while choosing a new parent.'
guard = '''\t// Resolve initial parent references only while choosing a new parent.
\tif mg.Spec.ForProvider.ApplicationID == nil && !consentscopes.HasInitializedParent(mg) {
'''
helper_import = '\t"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"\n'
updates = {}
found = 0
for path in sorted((root / 'apis').rglob('zz_generated.resolvers.go')):
    text = path.read_text()
    if method_marker not in text:
        continue
    found += 1
    if text.count(method_marker) != 1:
        raise SystemExit(f'Expected one consent reference resolver in {path}')
    start = text.index(method_marker)
    end = text.index('\n}', start) + 2
    method = text[start:end]
    if guard_marker in method:
        continue
    field = 'CurrentValue: reference.FromPtrValue(mg.Spec.InitProvider.ApplicationID),'
    last = '\tmg.Spec.InitProvider.ApplicationIDRef = rsp.ResolvedReference\n'
    if method.count(field) != 1 or method.count(last) != 1:
        raise SystemExit(f'Expected one initial parent reference block in {path}')
    field_start = method.index(field)
    block_start = method.rfind('\trsp, err = r.Resolve(', 0, field_start)
    if block_start < 0:
        raise SystemExit(f'Cannot locate initial reference resolution in {path}')
    block_end = method.index(last, field_start) + len(last)
    block = method[block_start:block_end]
    indented = ''.join('\t' + line if line.strip() else line for line in block.splitlines(keepends=True))
    method = method[:block_start] + guard + indented + '\t}\n' + method[block_end:]
    updated = text[:start] + method + text[end:]
    if helper_import not in updated:
        if updated.count('import (\n') != 1:
            raise SystemExit(f'Expected one reference resolver import block in {path}')
        updated = updated.replace('import (\n', 'import (\n' + helper_import, 1)
    updates[path] = updated
if found != 2:
    raise SystemExit(f'Expected both consent reference resolvers, found {found}')

# Adoption can skip a dangling initial reference before it has a cached ID.
# Conditional filtering of application_id alone cannot recognize that reference.
late_init_marker = '\tli := resource.NewGenericLateInitializer(opts...)\n'
late_init_guard = '''\t// Keep tracked parents creation-only after their defaults are removed.
\tinitialized, tracked := tr.GetAnnotations()["logto.crossplane.io/user-consent-scopes-initialized"]
\texternalName := tr.GetAnnotations()["crossplane.io/external-name"]
\tif tracked && externalName != "" && (initialized == "" || initialized == externalName) ||
\t\ttr.Spec.InitProvider.ApplicationIDRef != nil || tr.Spec.InitProvider.ApplicationIDSelector != nil {
\t\topts = append(opts, resource.WithNameFilter("ApplicationID"))
\t}

'''
terraformed = sorted((root / 'apis').rglob('zz_userconsentscopes_terraformed.go'))
if len(terraformed) != 2:
    raise SystemExit(f'Expected both consent Terraform method sets, found {len(terraformed)}')
for path in terraformed:
    text = path.read_text()
    if late_init_guard in text:
        continue
    if text.count(late_init_marker) != 1:
        raise SystemExit(f'Expected one consent late initializer in {path}')
    updates[path] = text.replace(late_init_marker, late_init_guard + late_init_marker)
for path, text in updates.items():
    path.write_text(text)
if updates:
    subprocess.run(['gofmt', '-w', *(str(path) for path in updates)], check=True)
print(f'Preserved initialization lifecycle for {found} generated consent APIs')
