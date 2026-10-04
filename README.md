# provider-upjet-logto

CCSN Crossplane provider generated with Upjet v2 from a checked-in source snapshot of the
`terraform-provider-logto` fork (based on Lenstra v0.0.15, MIT).
The controller embeds the Terraform Plugin Framework provider through protocol
v6; Terraform/OpenTofu is required only to regenerate the schema.

## Resources

Each resource has a namespaced API and a legacy cluster-scoped API, version
`v1alpha1`. Prefer namespaced resources on Crossplane v2.

| Terraform resource | Namespaced API group | Kind |
| --- | --- | --- |
| `logto_application` | `application.logto.m.crossplane.io` | `Application` |
| `logto_application_secret` | `application.logto.m.crossplane.io` | `Secret` |
| `logto_api_resource` | `api.logto.m.crossplane.io` | `Resource` |
| `logto_api_resource_scope` | `api.logto.m.crossplane.io` | `Scope` |
| `logto_role` | `role.logto.m.crossplane.io` | `Role` |
| `logto_user` | `user.logto.m.crossplane.io` | `User` |

References connect secrets to applications, scopes to APIs, roles to scopes,
and users to roles. Configure credentials with a `logto.m.crossplane.io/v1beta1`
`ProviderConfig` in the managed resource's namespace, or a `ClusterProviderConfig`.
The referenced Secret's `credentials` key contains JSON with `hostname`
(hostname only, no scheme), `resource` (Management API audience), `application_id`,
and `application_secret`. Use a machine-to-machine application authorized for
Management API `all`; for the default OSS tenant the audience is commonly
`https://default.logto.app/api` (verify it in your console).

`Application` exports `clientId` and existing named secrets as internal state
connection details. `Secret` exports `clientId`, `clientSecret`, and
`attribute.value` (reserved by Upjet for state reconstruction). Credential
values are absent from CRD spec/status. `writeConnectionSecretToRef` writes a
Secret in the resource's namespace. The `Secret` kind in this API group is a
Logto managed resource, distinct from a core Kubernetes Secret.

## Local development

The repository builds independently. `go.mod` points to the reviewed source
snapshot in `third_party/terraform-provider-logto`; its manifest records the
upstream base and SHA-256 of every included file. To update it from the adjacent
fork, run `make sync-provider`, review the diff, then regenerate and verify.
`make snapshot` detects accidental modifications to the embedded source.

```sh
nix develop path:.
make generate
make verify
```

`make generate` builds the fork, extracts its schema using a local development
override (no registry installation/init), scrapes local resource docs, and runs
Upjet, controller-gen and angryjet. `make verify` checks the snapshot, runs offline race tests and vet in both
modules, and builds a static controller under `.work/bin/provider`.
Caches stay in `../.local`; override `GOMODCACHE`, `GOCACHE`, or `GOPATH` if needed.
When the default Go proxy is unavailable, set `GOPROXY` to your preferred mirror.
Generated CRDs are in `package/crds`; runtime image definition is in
`cluster/images/provider-upjet-logto/Dockerfile`. The runtime image uses a
digest-pinned nonroot base. `make image` builds it
without accessing credentials. Distribution still requires an approved package
registry, signed release process, and a tested Crossplane package.

## Existing SSO adoption

Start with `examples/namespaced/application/import.yaml` and `managementPolicies:
[Observe]`; fill verified application IDs and named-secret IDs. New application
and named-secret examples are in `application.yaml`; they intentionally omit
Delete. Keep the existing consumer Secret distribution until the connection
Secret has been checked. No production Flux kustomization includes these files.

Named secrets and legacy application secrets are separate Logto APIs. This
provider does not rotate or import legacy secrets. Replacing a named-secret
resource explicitly replaces the owned credential; expiry and rotation policies
are not implemented yet. Application `client_secrets` is read-only and never
creates or rotates secrets. Terraform sensitive fields are still present in
Terraform state; use an encrypted and access-controlled backend.

## Validation and remaining scope

Offline tests cover all six resource lifecycles and Framework protocol v6
create/plan/update/read/delete, partial creation, pagination, bounded retries and cancellation,
token reuse/refresh/concurrency, sensitive schemas, idempotent deletion,
metadata preservation, credentials isolation, and status redaction. All 17
CRDs pass the Kubernetes API server structural/CEL validation functions.
Real acceptance tests use the `integration` build tag and temporary loopback-only
`LOGTO_TEST_CREDENTIALS`. `make test-integration` starts an isolated API Server
and executes the shipped controller. It validates the six-resource reference
graph, drift repair, restart recovery, Observe adoption, connection Secrets,
legacy cluster APIs and explicit empty role/scope revocation against Logto 1.40.1.
`KUBEBUILDER_ASSETS` must point to local etcd, kube-apiserver and kubectl binaries.

This is an initial implementation, not a completed migration of every SSO
setting. Connector configuration, sign-in experience, custom token claims,
organizations, and SAML-specific settings are not managed yet. The validated Logto version is 1.40.1. CI includes real Logto/controller acceptance
and amd64/arm64 package builds. Actual Crossplane Core installation and upgrade,
production RBAC, load/soak testing and signed distribution remain release gates.
Automatic credential rotation is not implemented.

Upstream references: [Terraform provider](https://github.com/Lenstra/terraform-provider-logto),
[Upjet template](https://github.com/crossplane/upjet-provider-template),
[Upjet](https://github.com/crossplane/upjet),
[Logto application-secret API](https://github.com/logto-io/logto/blob/v1.40.1/packages/core/src/routes/applications/application-secret.ts).

See [quality assessment](docs/quality-review.md)
for executed checks, findings and production readiness gates.

Embedded dependency updates belong to the Terraform fork. Dependabot does not
modify the snapshot directly: sync and review it after source dependency updates.

See [operations and release gates](docs/operations.md) for real acceptance, package verification, monitoring and recovery.

## Package development

```sh
nix develop --command make package
GOARCH=arm64 nix develop --command make package
```

Each build creates `.work/provider-upjet-logto.xpkg`, `runtime.spdx.json` and
`SHA256SUMS`. Packaging embeds the compiled controller, checks the non-root
runtime, CA certificates and all 17 CRDs, and inventories dependencies with
Syft. The local package builder does not need a container daemon.

## License and provenance

This repository derives from the Apache-2.0 Upjet provider template; see
[LICENSE](LICENSE). The embedded Terraform fork retains its MIT
[license](third_party/terraform-provider-logto/LICENSE). Source ownership and
upstream references are recorded in [NOTICE](NOTICE).
