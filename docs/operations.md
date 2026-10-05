# Logto provider operations and release gates

The supported implementation currently manages Application, application Secret,
API Resource, Scope, Role and User. It does not yet manage the complete Logto
configuration surface, such as connectors and sign-in experience.

## Validate a release candidate

1. Maintain the embedded fork in `terraform-provider-logto` first. Sync it with
   `make sync-provider`, review `third_party/terraform-provider-logto/snapshot.json`,
   then run `make generate` and review generated API/CRD changes.
2. Run `make verify`, `make lint`, `make security` and generation reproducibility
   checks. Required GitHub checks should include both Quality jobs in each project.
3. Use an isolated Logto 1.40.1 and PostgreSQL. Seed using the matching official
   Logto CLI. Generate local M2M credentials with the reviewed
   `scripts/bootstrap_acceptance.py` from the Terraform project; it requires
   `LOGTO_TEST_DB_URL` and only accepts loopback endpoints. Its `--stage=verify`
   checks credentials independently. Credentials stay in ignored `.work` files.
4. Run the Terraform project's `make test-integration` with
   `LOGTO_TEST_CREDENTIALS` pointing to that file. Run this project's
   `make test-integration` with the same variable and `KUBEBUILDER_ASSETS` pointing
   to a directory containing etcd, kube-apiserver and kubectl. The tests always
   create their own API server and never use an existing kubeconfig.
5. Run `make package`. It builds an embedded runtime archive without a container
   daemon, builds the XPKG with the Crossplane CLI, generates an SPDX SBOM and
   checks the binary digest, platform, non-root user, trust store and all 27 CRDs.
   `GOARCH=arm64 make package` builds the ARM candidate; rebuild for the host
   architecture before running native controller tests.
6. Before publishing, define the fork's registry and version, configure signing
   and provenance in the release environment, and execute an actual Crossplane
   package installation/upgrade on an isolated cluster. Gate publishing on all
   checks. Current CI prepares artifacts and does not publish releases.

The local API-server suite tests reconciliation and state recovery; it does not
run kubelets or the Crossplane package manager. Archive verification and native
controller execution do not prove container startup or package installation.

## Deploy and monitor

Use the example `DeploymentRuntimeConfig` to configure startup/liveness/readiness
probes, non-root execution, a read-only filesystem, resources and leader election.
Attach it to the Provider using `spec.runtimeConfigRef`. Use an immutable package
digest. Begin with Observe policies when adopting existing resources and verify
external names and parent IDs before enabling Create/Update/Delete policies.

Expose `/metrics` on 8080 only within the monitoring network. Probe `/healthz`
and `/readyz` on 8081. Alert on controller restarts, sustained reconciliation
errors, and managed resources remaining `Synced=False` or `Ready=False` beyond
three configured poll periods. Investigate by resource namespace/name and
external name. Credentials are loaded directly from Secrets by default so
rotation is visible to subsequent reconciles.

Give each Logto object one configuration writer. Metadata preservation uses
GET followed by PATCH; the API offers no demonstrated compare-and-swap contract.
Simultaneous edits through the console and GitOps can overwrite each other.
Connection Secrets contain client credentials and must have restricted RBAC,
backup access and encryption consistent with the cluster's existing policy.

## Failure and recovery

- **401/403:** inspect Secret reference, same-namespace rules, M2M grant and
  Management API audience. Fix the credentials/permissions; the external name
  remains intact. Authentication refresh is bounded and never follows redirects.
- **429/5xx:** reads retry within bounded limits and respect Retry-After. Inspect
  API availability and lower reconciliation rate when needed. Mutations are not
  blindly retried after ambiguous failures.
- **Creation response lost:** inspect Crossplane's external-create annotations
  and the Logto object before removing pending markers or retrying. For named
  secrets the external name and Terraform import identifier are
  `application-id/secret-name`. Terraform scope import uses `resource-id/scope-id`,
  while a Crossplane Scope external name is only `scope-id`; set its parent through
  `spec.forProvider.resourceId` or `resourceIdRef`. Other resources use the returned
  remote ID. Adopt
  under Observe first. Never remove an external name simply to rerun creation.
- **Partial role-scope update:** obsolete permissions are revoked first. Restore
  API access and let reconciliation add desired permissions. Role identity and
  user membership remain stable; a partial failure can temporarily reduce grants.
- **Controller restart:** desired state, external names and connection Secrets
  reconstruct provider state. Never depend on local Terraform state files.
- **Secret rotation:** create a new named Secret and a separate connection Secret,
  switch consumers, verify token issuance, then retire the old secret according
  to the approved cutover. Expiration scheduling and consumer cutover automation
  are not implemented by this resource.
- **Upgrade rollback:** preserve CRDs, managed resources, ProviderConfigs and
  connection Secrets; restore the previous immutable provider package digest.
  Test schema compatibility and Observe behavior before enabling writes. Binary
  rollback does not undo Logto configuration changes; restore those through a
  reviewed desired-state change.

## Support matrix and evidence

Local acceptance used Go 1.26.8, Logto 1.40.1, PostgreSQL 17.11, Kubernetes API
Server 1.37.0 and etcd 3.6.14. The provider embeds Crossplane runtime 2.4.2 and
Upjet stable 2.5.1. A Core Crossplane installation/upgrade matrix
still requires the release cluster gate. Terraform protocol v6, six resource
lifecycles/imports, pagination beyond 100 objects, metadata preservation, CRD
references, Observe behavior, authorization failure, drift repair, restart,
connection Secret publication and explicit empty-role revocation have local
execution evidence. Remote GitHub CI has not been dispatched from this workspace.

## OCI releases and OIDC verification

Pushing a new `vMAJOR.MINOR.PATCH` tag triggers `release.yml`. The reusable
quality workflow must pass race tests, static analysis, reproducible generation,
real Logto acceptance, shipped-controller acceptance and package verification
before publication. The release combines the verified amd64 and arm64 packages
into one GHCR OCI index. Existing version tags are never overwritten.

Cosign obtains an ephemeral signing identity from GitHub Actions OIDC, signs the
index and both platform digests, and verifies each signature against the exact
release workflow/tag identity and `https://token.actions.githubusercontent.com`.
Rekor transparency verification remains enabled. The workflow uploads signature
verification evidence and records the immutable package reference in its summary.

Before installing, verify the recorded digest with the matching release version:

```sh
nix develop --command cosign verify \
  --certificate-identity https://github.com/the-ccsn/provider-upjet-logto/.github/workflows/release.yml@refs/tags/v0.1.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/the-ccsn/provider-upjet-logto@sha256:<released-index-digest>
```

Use that digest in `Provider.spec.package`. Signature verification is an explicit
release/deployment check; publishing a signature does not by itself configure
Crossplane to enforce signatures. Consumers need anonymous GHCR pull access or
an appropriate image pull Secret when the package is private.
