# ARD

Optional [Agentic Resource Discovery v0.91](https://agenticresourcediscovery.org/spec)
support for Covonaut. This package discovers resource descriptions; MCP and A2A
remain the execution protocols. It does not implement a Registry service.

## Scope

| Surface | Support |
| --- | --- |
| Entries and manifests | Extensible media types, semantic validation, unknown-field preservation |
| JSON-LD entry expansion | Base/remote/imported/protected/scoped contexts, graphs, reverse properties, language/index/ID/type containers; standard-library implementation |
| Publishing | Immutable GET/HEAD manifest handler, ETag, conditional requests |
| Web discovery | `/.well-known/ard.json`, HTML `rel="ard"` links and document base, inline aliases/graphs/included nodes |
| Registry search | Search request/response, filters, federation modes, opaque pagination |
| Referrals | Explicit bounded traversal, cycle detection, per-registry responses |
| Agent integration | Read-only `discover_resources` tool, application-configured registries |
| Resource use | Application-authorized A2A 1.0/MCP HTTP connections; Skill text description only |

Not included: Registry indexing/ingestion, Explore/List, robots/DNS discovery,
legacy discovery URLs, trust signature verification, stdio launch, Skill
installation, or a global search ranking. v0.91 is a proposal, not a claim of
certified compatibility with every Registry implementation.

No external dependencies are added for ARD. The built-in processor implements
JSON-LD entry expansion: ordered contexts, prefixes/aliases, type/JSON coercion,
imported and protected contexts, property/type scopes and propagation, graphs,
reverse/nested properties, language/direction and index/ID/type maps, and
set/list containers. Remote contexts remain bounded and use the controlled
transport. Context reset preserves the required ARD initial context. Original
entries preserve unknown terms and opaque JSON numbers without rounding.

`ExpandDocument` accepts arrays and graph documents; `Expand` and `Core` handle
individual entries. This is an entry-expansion API, not RDF canonicalization,
framing, or signing tooling. The tests cover the supported protocol behavior;
no independent JSON-LD or ARD conformance certification is claimed.

## Conformance

The pinned official W3C expansion manifest is exercised offline: 368 applicable
cases pass (273 positive, 95 rejection cases); 18 officially marked JSON-LD
1.0-only cases are skipped. All 95 negative cases require the exact standard
JSON-LD error code, exposed through `JSONLDError.Code` and `errors.As`;
`Unwrap` preserves underlying causes. Default tests retain local regressions including official
identifier-coercion and blank-node fixtures.

Official ARD checks were actually executed: base-context comparison, strict
Schema CLI validation of the publishing handler's URL-backed and inline entries,
21 independent Draft 2020-12 structural cases, and seven cases against the
unmodified CDDL. These checks are opt-in and fail if configured validators do not
run; absent environment variables produce explicit skips. Test tools are outside
the repository, with no added runtime dependencies.

See [conformance evidence and requirement traceability](https://github.com/covoyage/covonaut/blob/main/ard/CONFORMANCE.md)
for pinned revisions, full reproduction commands, application/Registry ownership,
and tested upstream Schema/CDDL discrepancies. In particular, the CDDL permits
both delivery members, and the Schema misspells `trustManifest`; passing either
artifact alone is not full ARD certification.

To run the publishing check with an already configured official checkout:

```sh
ARD_CONFORMANCE_BIN=/path/to/ard-spec/conformance/bin/conformance-test go test ./ard -run TestOfficialARDManifestConformance -v -count=1
```

The executable comes from the [official ARD repository](https://github.com/ards-project/ard-spec).
Its environment must already support strict JSON Schema checking, with the
official schema present in that checkout. The test fails if the CLI skips schema
validation, even when its exit status is successful. Nothing is downloaded or
installed by the tests; without the environment variable, this optional test
is explicitly skipped. The additional Schema/CDDL/W3C checks have their own
environment variables documented in the conformance report.

## Publishing

```go
handler, err := ard.NewManifestHandler(ard.Manifest{Entries: []ard.Entry{entry}})
if err != nil {
    return err
}
mux.Handle(ard.ManifestPath, handler)
```

The handler snapshots the manifest and validates its JSON-LD semantics without
network access. Use the pinned base context or inline contexts when publishing.
`NewManifestHandlerWithContext` accepts a configured processor for remote
contexts or application-proven identity mappings.
It sets `Cache-Control: public, max-age=300`; publish only public descriptions.
The application supplies the HTTP server, authentication, and any credentialed
or differently cached publishing endpoint.

`ValidateEntry` validates ordinary canonical fields and returns warnings for
recommendations such as 2-5 representative queries. Use
`ContextProcessor.Validate` for entries with custom contexts, and `Core` to
obtain fields after expansion. `Expand` exposes expanded IRI values.
Original entries retain unknown fields and opaque JSON; nested opaque fields
are not recursively expanded.
Decoding also preserves explicitly empty or null `url` members. They are not
treated as absent when checking the exactly-one-of-`url`-or-`data` rule, and
serialization does not silently remove them.
Resource media types are checked against RFC 6838 type/subtype name syntax,
including the 127-character limit. Wildcards are not resource media types;
valid extension types and MIME parameters are accepted without requiring IANA
registration.

An `@id` equal to `identifier` needs no external proof. Conflicting `urn:air:`
handles and non-absolute IDs are rejected. For a different absolute resource
IRI, consumers require `ContextProcessor.VerifyIdentity` or
`ClientConfig.VerifyIdentity`; the application proves that both names denote
the same resource. Missing or failed proof rejects the entry. `ValidateEntry`
alone performs structural checks and warns about an unproven IRI mapping.

## Discovery and Search

```go
client := ard.NewClient(ard.ClientConfig{})
defer client.Close()
manifest, err := client.Discover(ctx, "https://publisher.example.com")
if err != nil {
    return err
}
response, err := client.Search(ctx, "https://registry.example.com/api", ard.SearchRequest{
    Query: ard.Query{Text: "an agent that summarizes financial reports"},
    Federation: ard.FederationNone,
    PageSize: 10,
})
if err != nil {
    return err
}
```

`Discover` uses the origin root and honors HTML links even when the well-known
manifest exists. Inline resources are identified by expanded ARD terms, not
literal field names, including nodes inside `@graph` and `@included`. Unrelated
markup is ignored, and opaque JSON is never crawled as resource nodes.
`FetchManifest` reads a specific manifest URL. Remote contexts
use the same network policy, response limits, and bounded loader.

`Search` appends `/search` to a Registry base URL. `SearchEndpoint` consumes an
exact referral URL without appending a path. Filters preserve the query context
and scalar/array values for Registry-side IRI matching. Nulls and mixed-type
filter arrays are rejected before any network request. Pass each response's
opaque `PageToken` back to that same Registry. Sparse search results are not
complete entries and cannot be directly resolved for execution.

`Search` never follows referrals itself; omitted federation mode delegates the
protocol's `auto` behavior to the Registry. `SearchFederated` explicitly requests
`referrals`, traverses at most two hops/eight registries by default, and preserves
each Registry's scores and pagination separately. Failures at referred registries
are reported in `RegistryResults.Error`; root failure returns an error. The
client bounds response bytes, entry counts, redirects, and operation time.

## Agent Extension

```go
extension, err := ard.NewDiscoveryExtension(ard.ExtensionConfig{
    Client: client,
    Registries: map[string]string{"public": "https://registry.example.com"},
})
if err != nil {
    return err
}
cfg.Extensions = append(cfg.Extensions, extension)
```

The tool accepts a query, configured Registry name, optional media type, page
size, and page token. It uses `federation: none`; it cannot take arbitrary
Registry URLs, connect to results, register executable tools, or install Skills.
The caller owns `client` and closes it after all agents and connections finish.

## Authorization

HTTPS/public addresses are required by default. The client disables proxies,
checks DNS answers at actual dial time, connects to checked IPs, validates every
redirect, and scopes credentials through `ClientConfig.HeadersByOrigin` keyed by
`scheme://host[:port]`. `AllowedHosts` is an exact hostname allowlist, not a trust
statement. `AllowHTTP` and `AllowPrivate` are explicit development/intranet opt-ins.

`ResolveResource` requires an application `AuthorizeFunc` before reading a card.
The returned `TrustStatus` is always `unverified`: a score is relevance, not trust.
Structural identity checks are not signature, DID, or publisher-ownership
verification; perform those checks in your application before authorizing use.

The default endpoint resolver reads A2A 1.0 `supportedInterfaces` (`JSONRPC` or
`HTTP+JSON`). The ARD entry URL denotes a card, not necessarily its invocation
URL. Other card formats require `ResolveConfig.Resolver`; for MCP it must return
the documented HTTP endpoint with `Binding: "MCP_HTTP"`. Missing/incompatible
interfaces return errors, not guessed URLs. No stdio commands are launched.

`ConnectA2A` and `ConnectMCP` require separate `connect` approval and retain the
client's controlled transport and response limits. MCP connection initializes a
session but does not import tools into an Agent automatically. Configure
credentials at the approved origin. Card, Skill, and execution redirects require
approval for the new URL as well. `DescribeSkill` only returns bounded text
from a URL-backed Skill after `describe` approval; never treat that text as
trusted instructions or automatically install/execute it.

## Offline Example

```sh
go run ./example/ard
go test ./ard -race
```

The example uses temporary loopback HTTP fixtures, including a fixed search
response, not a production Registry implementation. It needs no model keys and
performs no remote execution. See [example source](https://github.com/covoyage/covonaut/tree/main/example/ard).