# ARD Conformance Evidence

Covonaut is an Agent framework. Its optional ARD package implements publisher,
consumer, and application-authorized resource integration, not a Registry.
This document maps ARD v0.91 requirements to implementation and executable
evidence. It is not an independent certification or a claim about every Registry.

## Verified Artifacts

Verified on 2026-10-14 against pinned, unmodified upstream artifacts:

| Source | Revision / Tool | Evidence |
| --- | --- | --- |
| [ARD specification](https://github.com/ards-project/ard-spec) | `49eb4df6f3841ccecc5c31d5175117337b568bdf` | Official base context, JSON Schema, CDDL, and conformance CLI |
| JSON Schema Draft 2020-12 | Python `jsonschema` 4.25.1 | Official CLI checks the actual publisher's URL-backed and inline entries; 21 independent structural positive/negative cases |
| CDDL | Rust `cddl` 0.10.7 | Original `ard.cddl`, without rewriting; seven cases including two documented upstream discrepancies |
| [W3C JSON-LD API expansion suite](https://github.com/w3c/json-ld-api) | `55514738d854e56bb8bad4c53cda2f0e23be4a50` | 386 manifest cases: 368 passed, 18 JSON-LD 1.0-only cases skipped, zero failures |

The 368 applicable W3C cases comprise 273 positive expansions and 95 negative
inputs that must be rejected with their exact standard JSON-LD error codes.
The harness requires a typed `JSONLDError` and compares its `Code` directly with
the official `expectErrorCode`; an unrelated error is not a successful rejection.
Skips are determined only by official
`specVersion: json-ld-1.0` or `processingMode: json-ld-1.0` markers. All other
manifest cases are run. Remote fixtures use an offline transport, not the public
network. Array comparison preserves list order and opaque JSON value contents.

The W3C harness exercises the generic expansion engine with an empty initial
context. Separate ARD tests exercise the public processor's mandatory ARD
initial context, including preservation across context reset. Applications can
inspect semantic error codes with `errors.As(err, &jsonLDError)`. `JSONLDError`
retains its underlying `Err` through `Unwrap`, including cancellation/deadline
causes. Local tests cover nested scoped-context errors and protected `@type`
definitions as well as ordinary Go cancellation behavior.
There is no claim of complete JSON-LD API conformance: compaction, flattening,
framing, RDF conversion,
canonicalization, signing, and JSON-LD 1.0 API processing are not exposed here.
The independent Schema tests check structural constraints, not optional `format`
assertions; media type syntax, absolute URLs, FQDNs, and identity semantics are
checked separately by the Go implementation and its tests.

Tests never download artifacts or install dependencies. Without their explicit
environment variables, external checks are visibly skipped, not considered
successful. If configured validators fail to launch, skip Schema checks, or
produce unexpected output, the tests fail. CDDL diagnostics are checked because
the verified CLI can report a validation error while exiting successfully.

## Requirement Traceability

"Supported" means the framework-facing requirement is implemented and tested.
"Delegated" means an application or the declared trust framework owns that
decision. Registry-only and optional surfaces are deliberately outside scope.

| Requirement | Status / Owner | Executable Evidence |
| --- | --- | --- |
| 3.3: Open RFC 6838 media types, including extensions; do not demand IANA registration | Supported: [ValidateEntry](types.go) | `TestEntryMediaTypeRestrictedNames` |
| 3.4, 4.1: Apply ARD initial context before entry context; permit omitted context, prefixes, aliases, and overrides | Supported: [ContextProcessor.ExpandDocument](context.go) | `TestContextExpansion`, `TestCoreContextSemantics`, `TestContextArraysAndForwardDefinitions` |
| 4.1: Context reset does not remove ARD initial context | Supported: [contextExpansion.apply](context.go) | `TestJSONLDProtectedTermsAndNullContext` |
| 4.1, 5.3.1: Preserve unknown terms and opaque `data`, `metadata`, `trustManifest` JSON | Supported; see Schema discrepancy below: [Entry JSON methods](types.go), [Core](context.go) | `TestEntryRoundTrip`, `TestContextCoercionAndOpaqueJSON`, `TestHTMLDiscoveryAliasesGraphsAndOpaqueData` |
| 4.1: JSON-LD expansion, scopes, propagation, graphs, reverse/nest properties, list/set and map containers | Supported expansion behavior: [contextExpansion](context.go) | `TestW3CExpansionSuite`; local scoped/container/reverse/nest tests in [context_test.go](context_test.go) |
| 4.1: Standard expansion rejection codes and inspectable Go error chains | Supported: [JSONLDError](context.go), `Code`, `errors.As`, and `Unwrap` | All 95 official negative cases compare exact codes; `TestJSONLDErrorCodes`, `TestJSONLDErrorUnwrap` |
| 4.1: Remote contexts, relative URLs, imports, protected terms; bounded recursion and loading | Supported: [contextExpansion.apply](context.go), controlled Client transport | `TestRemoteContext`, `TestJSONLDImportContext`, `TestRemoteContextRedirectBaseCyclesAndBudget`, `TestTermDefinitionDepthLimit` |
| 4.2: Require `identifier`, `displayName`, `type`, and one delivery term | Supported: [ValidateEntry](types.go), [ContextProcessor.Validate](context.go) | `TestValidation`, `TestManifest`, `TestOfficialARDSchemaConstraints` |
| 4.2, D.2: 2-5 representative queries are a recommendation, not a hard error | Supported: validation warnings | `TestValidation`, `TestOfficialARDSchemaConstraints/queries-are-recommended` |
| 4.2: Optional capabilities and descriptive extension terms | Supported: open Entry model | `TestEntryRoundTrip`, `TestOfficialARDSchemaConstraints/open-namespace` |
| 4.3: Exactly one of `url` / `data`, including explicitly empty or null URL members | Supported: [Entry decoding and ValidateEntry](types.go) | `TestValidation`, `TestEntryValueOrReferencePresence`; official Schema `both-deliveries`, `null-reference`, `null-data`, `array-data` |
| 4.5: Open trust envelope; require identity when envelope is present | Supported structural checks: [ValidateEntry](types.go) | `TestTrustAndReservedFields`, `TestEntryRoundTrip` |
| 4.5.1, C: Publisher-authority binding | Structural SPIFFE/HTTPS domain alignment supported; cryptographic proof and other identity schemes delegated | `TestEntryIdentityBinding`, `TestTrustAndReservedFields`; application authorization remains required |
| 4.5.2: Verify declared trust framework, attestations, provenance, keys, and signatures | Delegated; no Registry verification service or trust certification | `TestA2AResolutionAndAuthorization`, `TestTrustAndReservedFields`; resolved resources remain `unverified` |
| 4.5.2, 5.3.2: Score is relevance only, never a trust/compliance/safety rating | Supported: [ResolvedResource](resource.go); no global trust score or automatic execution | `TestTrustAndReservedFields`, `TestA2AResolutionAndAuthorization` |
| 5.1: MUST fetch `/.well-known/ard.json` and MUST honor `rel="ard"` | Supported: [Client.Discover](client.go) | `TestPublishAndDiscover`, `TestHTMLDiscovery` |
| 5.1: Manifest requires an `entries` array; other top-level fields are transport-defined | Supported: [Manifest JSON methods](types.go), [FetchManifest](client.go) | `TestManifest`, official Schema manifest/empty/missing-entries cases |
| 5.1: Publish discoverable descriptions | Supported immutable GET/HEAD publisher, ETag, conditional responses: [NewManifestHandler](publish.go) | `TestPublishAndDiscover`, `TestOfficialARDManifestConformance`, `TestManifestPublishingWithVerifiedResourceIRI` |
| 5.1: In-page JSON-LD and HTML document base | Supported: [Client.Discover](client.go) | `TestHTMLDiscoveryAliasesGraphsAndOpaqueData`, `TestHTMLDiscoveryRespectsDocumentBase` |
| 5.1: Predecessor paths/relations, Agentmap/robots, DNS SVCB | Optional, not implemented | Not required for the supported consumer profile |
| 5.2: Web ingestion of manifests and in-page markup | Entry-source fetching supported; persistent crawling/indexing is Registry-only | Discovery tests above; no Registry infrastructure is implemented |
| 5.2: Git/npm/OCI ingestion | Optional Registry-only infrastructure | Not implemented |
| 3.5, 5.3: Registry MUST expose REST search | Registry-only server obligation; HTTP consumer supported | `TestSearchAndReferralEndpoint`; no ARD search server is claimed |
| 5.3: Entry URL denotes Registry base; referral URL is complete search endpoint and MUST NOT have `/search` appended | Supported: [Search / SearchEndpoint](client.go) | `TestSearchAndReferralEndpoint`, `TestFederationBudgetsAndCycles` |
| 5.3.1: Preserve query context, filter keys, scalar/array constraints, and literal opaque-member paths | Supported wire encoding; IRI matching, AND/OR evaluation, derived publisher filtering, and indexed-term policy are Registry decisions | `TestSearchAndReferralEndpoint`, `TestSearchRejectsNullAndMixedFiltersBeforeNetwork`; [Query](types.go) |
| 5.3.2: POST search, required text, federation modes, page size, opaque page token | Supported: [SearchEndpoint](client.go) | `TestSearchAndReferralEndpoint`, `TestSearchRejectsNullAndMixedFiltersBeforeNetwork`, `TestFederationBudgetsAndCycles` |
| 5.3.2: Results require only identifier; other fields may be absent | Supported sparse [SearchResult](types.go); full entry validation not imposed on sparse results | `TestSparseSearchResult`, `TestSearchAndReferralEndpoint` |
| 5.3.2: Scores, when supplied, are 0-100; preserve source and Registry pagination | Supported; ranking and token generation are Registry-only | `TestLimitsErrorsAndCancellation`, `TestSparseSearchResult`, `TestFederationBudgetsAndCycles` |
| 5.3.2: Artifact URL is not a normative identifier-to-entry lookup | Supported separation; no invented lookup operation | `TestSparseSearchResult`, `TestA2AResolutionAndAuthorization` |
| 5.3.3-5.3.5, A: Explore, List/filter grammar, Registry protocol wrappers | Optional Registry-only surfaces, not implemented | The read-only Agent extension is not advertised as a Registry wrapper/server |
| 5.4: `auto`, `referrals`, `none`; client chooses which referrals to follow | Supported: [SearchFederated](federation.go), ordinary Search does not follow referrals | `TestFederationBudgetsAndCycles`, `TestDiscoveryExtensionIsReadOnly` |
| B: Standard HTTP statuses and `errorCode` / `message` | Supported: [Client.requestWithHTTP](client.go), [ProtocolError](types.go) | `TestStandardProtocolErrors`, `TestLimitsErrorsAndCancellation` |
| C: AIR URN has FQDN publisher and terminal name; namespace optional and multisegment | Supported: [Publisher](types.go) | `TestValidation`, `TestEntryIdentityBinding` |
| C: `@id` and discovery identifier MUST denote same resource | Supported structural rejection and application equivalence proof: [verifyIdentity](context.go) | `TestIdentityEquivalenceVerifier`, `TestClientIdentityVerifierCoversAllConsumerPaths`, `TestEntryIdentityBinding` |
| D.1-D.2: Open entry/manifest schema, base context, structural grammar, discovery constraints | Independent artifact checks plus local semantic tests; discrepancies below | `TestOfficialARDManifestConformance`, `TestOfficialARDBaseContext`, `TestOfficialARDSchemaConstraints`, `TestOfficialARDCDDLConformance` |
| D.3-D.4: OpenAPI / live Registry certification | Registry-only; no live Registry server certification claimed | The official CLI is exercised in manifest mode, not Registry mode |

Local framework tests additionally cover origin-scoped credentials, dial-time
address checks, redirects and reauthorization, response/context/federation
budgets, cancellation, read-only Agent discovery, A2A 1.0 endpoint selection,
application-supplied MCP endpoint resolution, and describe-only Skill handling.
These controls do not automatically install resources or trust discovered text.

## Upstream Discrepancies

The pinned artifacts are not mutually equivalent. They are not modified or
silently treated as universal correctness oracles:

1. JSON Schema uses capitalized `TrustManifest` in `EntryFields.properties`.
   The context, normative prose, and CDDL use `trustManifest`. The Schema accepts
   an empty lowercase envelope as an unknown field, while Covonaut requires its
   identity. Both Schema outcomes are explicitly tested.
2. The CDDL's open wildcard admits the other delivery member after choosing
   `url` or `data`, so the verified validator accepts an entry carrying both.
   The authoritative JSON Schema and normative rule reject it; Covonaut rejects
   it at decoding/validation/publishing paths. The CDDL test records acceptance
   as an upstream discrepancy, **not** as a valid ARD entry.
3. CDDL requires nonempty `tags` and `capabilities`; JSON Schema allows empty
   arrays. Both artifact outcomes are tested. Covonaut follows the authoritative
   Schema's optional-array behavior.
4. JSON Schema restricts metadata values to scalars. The base context marks
   metadata as opaque JSON; Covonaut preserves nested metadata without expanding
   or discarding it. Nested metadata therefore may not pass the pinned strict
   Schema. Publishers requiring that Schema's structural profile should use
   scalar metadata values. Its rejection is explicitly tested.

The CDDL header declares JSON Schema authoritative when they disagree. Structural
validation alone does not establish identity ownership, signatures, relevance,
runtime safety, or interoperability with a live Registry.

## Reproduce

Run from the repository root. Prerequisites: Go, `curl`, `tar`, Python 3 with
venv/pip support, and a Rust toolchain with Cargo. Network access is needed only
for this explicit setup. Validators and fixtures are installed outside the
repository; no Go modules or runtime dependencies are added. No Git command is
needed.

```sh
verify_dir="$(mktemp -d "${TMPDIR:-/tmp}/covonaut-ard.XXXXXX")"
ard_revision=49eb4df6f3841ccecc5c31d5175117337b568bdf
w3c_revision=55514738d854e56bb8bad4c53cda2f0e23be4a50
curl -fsSL "https://codeload.github.com/ards-project/ard-spec/tar.gz/$ard_revision" -o "$verify_dir/ard.tar.gz"
curl -fsSL "https://codeload.github.com/w3c/json-ld-api/tar.gz/$w3c_revision" -o "$verify_dir/w3c.tar.gz"
tar -xzf "$verify_dir/ard.tar.gz" -C "$verify_dir"
tar -xzf "$verify_dir/w3c.tar.gz" -C "$verify_dir"
python3 -m venv "$verify_dir/venv"
"$verify_dir/venv/bin/python" -m pip install 'jsonschema==4.25.1'
cargo install cddl --version 0.10.7 --locked --root "$verify_dir/tools"
export PATH="$verify_dir/venv/bin:$PATH"
export ARD_SPEC_DIR="$verify_dir/ard-spec-$ard_revision"
export ARD_CONFORMANCE_BIN="$ARD_SPEC_DIR/conformance/bin/conformance-test"
export ARD_SCHEMA_PYTHON="$verify_dir/venv/bin/python"
export ARD_CDDL_BIN="$verify_dir/tools/bin/cddl"
export ARD_W3C_TEST_DIR="$verify_dir/json-ld-api-$w3c_revision/tests"
go test ./ard -run 'TestOfficialARD|TestW3CExpansionSuite' -v -count=1
go test ./ard -race -count=1
go test ./...
```

`TestW3CExpansionSuite` names each official manifest ID and explains every skip.
Do not count external tests as verified when their environment variables were
unset. Do not infer full consumer/Registry conformance from a successful manifest
CLI invocation alone.