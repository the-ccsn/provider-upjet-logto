# Local migration validation — 2026-10-05

Validation was completed locally after the initial CCSN source publication,
before publishing the migration fixes. The provider now has eleven Terraform
resources and 27 shipped CRDs. This report records local verification; it does
not establish OCI publication or production reconciliation.

| Check | Result |
|---|---|
| Terraform `make verify` | Passed: race, offline protocol lifecycles, vet and build |
| Terraform real Logto acceptance | Passed against disposable Logto 1.40.1, including five configuration contracts |
| Upjet `make verify` | Passed: vendored source checksums, race tests, structural/CEL validation of 27 CRDs, vet and build |
| Actual shipped controller | Passed: isolated API server, real six-resource graph, drift, restart, legacy APIs, empty relation revocation |
| Role-only user adoption | Passed: importing an existing user with only ID and role references preserves existing username, email, name and profile |
| Four singleton configuration controllers | Passed: Observe makes no API changes; Update modifies the owned fields; cleanup restores original tenant settings |
| Connector controller | Passed: SecretRef creation and update; sensitive configuration excluded from status |
| staticcheck / govulncheck | Passed for both providers; no known vulnerabilities reported |
| GitOps migration | Passed: 50 imports, exact snapshot parity for apps/settings/roles/users/registry IDs, schema checksums, field names, references, encrypted input Secrets, suspended entrypoints |
| GitOps secret scan | Passed with a narrow allowlist for manifest SHA256 values; no known exported credential literals in manifests |
| GitOps CI syntax | Passed actionlint; new offline migration validator added to the existing workflow |
| Generation reproducibility | Passed: repeating each provider's generation produced identical file hashes |
| Local amd64 / arm64 packages | Passed: embedded executable hash, non-root user, CA trust store, all 27 CRDs and SPDX SBOM; artifacts under `.work/release/migration-{amd64,arm64}` |

New regressions cover explicit false and zero metadata values, preservation of
unowned server fields, rejection of duplicate metadata ownership and arbitrary
configuration endpoints, required sensitive selector resolution, singleton
release semantics and connector credential redaction. The required connector
initProvider selector is generated as an optional pointer to prevent an unset
selector from shadowing its valid forProvider counterpart.

Crossplane's dependency retry delay is capped at 60 seconds. The integration
assertion budget now permits multiple capped retries rather than expiring at
that cap, and failure diagnostics include resource identity and conditions.
Newly created resources with server-generated IDs retain LateInitialize because
the current Upjet async controller uses that policy to persist external-name.
Observe-only imports provide their existing external ID and need no such grant.

Race coverage profiles report 63.9% across the Terraform provider (1,323/2,071
statements) and 1.8% across Upjet (95/5,336). Generated controller and API code
dominates the Upjet denominator; the subprocess acceptance suite executes the
real binary without contributing to that unit-test profile. These figures are
not a claim of full behavioral coverage, and are not directly comparable to
the exclusions used in the historical audit. Raw profiles and acceptance logs
remain in ignored workspace files.

The published Terraform Quality run passed. The published Upjet quality job
passed but its real graph integration failed while waiting for User. Local
changes now pass the corresponding tests; remote CI cannot be marked fixed
until the run for the published fixes completes successfully.

Production gates remain: actual Crossplane Core installation and upgrade, live
snc reconciliation and ownership transfer, least-privilege RBAC review, load and
soak testing, signed OCI release, a permanent Logto M2M credential, and consumer
connection-Secret distribution. The GitOps preview intentionally retains the
existing consumer SOPS files and Terraform state. It is not a completed live
SSO cutover.
