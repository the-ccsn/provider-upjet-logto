#!/usr/bin/env python3
"""Retain explicit [] in optional collections while omitting nil values.

Go's omitzero preserves the distinction in Kubernetes JSON. The Terraform
jsoniter encoder does not support omitzero, so its tag intentionally emits
null for nil and [] for non-nil empty collections. Both map correctly to the
Framework's optional/computed collection semantics.
"""
from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parents[1]
fields = {'user_scopes', 'redirect_uris', 'post_logout_redirect_uris', 'cors_allowed_origins', 'scope_ids', 'role_ids'}
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

# mergo treats [] as missing even with overwrite disabled. An explicitly managed
# user scope set, including [], must take precedence over initProvider defaults.
merge_call = '\terr = mergo.Merge(&params, initParams, mergo.WithSliceDeepCopy, func(c *mergo.Config) {'
consent_guard = '''\t// Preserve the initialized parent until a missing resource starts a new creation.
\tif initialized := tr.GetAnnotations()["logto.crossplane.io/user-consent-scopes-initialized"]; initialized != "" &&
\t\tinitialized == tr.GetAnnotations()["crossplane.io/external-name"] && params["application_id"] == nil {
\t\tinitParams["application_id"] = initialized
\t}

\t// Explicit user scopes take precedence over initialization defaults, including [].
\tif scopes, present := params["user_scopes"]; present && scopes != nil {
\t\tdelete(initParams, "user_scopes")
\t}

'''
for path in sorted((root / 'apis').rglob('zz_userconsentscopes_terraformed.go')):
    text = path.read_text()
    if consent_guard in text:
        continue
    if text.count(merge_call) != 1:
        raise SystemExit(f'Expected exactly one initProvider merge in {path}')
    path.write_text(text.replace(merge_call, consent_guard + merge_call))

# The Framework runtime has no ignore_changes support for initProvider. Attach
# the handwritten consent lifecycle adapter to both generated controllers.
controllers = sorted((root / 'internal/controller').rglob('application/userconsentscopes/zz_controller.go'))
if len(controllers) != 2:
    raise SystemExit(f'Expected both consent scope controllers, found {len(controllers)}')
for path in controllers:
    text = path.read_text()
    if 'consentscopes.WrapConnector(' in text:
        if 'consentscopes.PreserveCreateIdentity(' not in text:
            raise SystemExit(f'Regenerate consent controller before preserving partial identity: {path}')
        continue
    marker = 'managed.WithExternalConnecter('
    api_import = re.compile(r'(\tv1alpha1 "[^"]+"\n)')
    if text.count(marker) != 1 or len(api_import.findall(text)) != 1:
        raise SystemExit(f'Expected one external connector and API import in {path}')
    start = text.index(marker) + len(marker)
    depth = 1
    end = start
    while depth:
        if text[end] == '(':
            depth += 1
        elif text[end] == ')':
            depth -= 1
        end += 1
    argument = text[start:end - 1].strip()
    wrapped = ('\n\t\t\tconsentscopes.WrapConnector(mgr.GetClient(), '
               'o.Features.Enabled(features.EnableBetaManagementPolicies),\n'
               '\t\t\t\tconsentscopes.PreserveCreateIdentity(\n'
               + argument + ',\n'
               '\t\t\t\t\to.Provider.Resources["logto_application_user_consent_scopes"].TerraformPluginFrameworkResource, o.OperationTrackerStore,\n'
               '\t\t\t\t),\n\t\t\t),\n\t\t')
    text = text[:start] + wrapped + text[end - 1:]
    text = api_import.sub(r'\1\t"github.com/the-ccsn/provider-upjet-logto/internal/consentscopes"\n', text, count=1)
    path.write_text(text)
    subprocess.run(['gofmt', '-w', str(path)], check=True)
