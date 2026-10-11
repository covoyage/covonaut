# Contributing to Docs

## Local Development

Use Node.js 22 LTS and Go 1.25 or later. From the repository root:

```sh
npm --prefix docs ci
npm --prefix docs run dev
```

The dev server prints a local URL under `/covonaut/`. To check production output:

```sh
npm --prefix docs run check
npm --prefix docs run preview
```

`check` tests language routing, builds the site, verifies local links and anchors,
checks bilingual page/heading/example parity, and compiles complete
Go programs found in rendered code blocks. The four A2UI fragments are also
compiled and run with offline fixtures, including missing-surface failures.
Two workflow fragments are also compiled and run to verify child-agent cleanup.
Other short fragments are not compiled;
keep them small and link to a complete example. These checks do not make live
provider calls or validate external websites.

## Content Ownership

- Site tutorials live in `docs/`.
- Complete runnable examples live in `example/` and are included as snippets.
- A2A and A2UI pages include their package README files directly. Edit the package
  README to update both surfaces.
- Exported API signatures belong in Go documentation and pkg.go.dev.

Update the sidebar when adding a page. Check model and provider claims against
the implementation; clearly distinguish native support from prompt fallbacks.

## Languages

English pages retain their original paths; matching Chinese pages live in
`docs/zh/`. Add both versions together. Chinese headings use the corresponding
English anchor IDs so language switching preserves the current section.
Complete Go programs reuse the same source, including selected Markdown ranges;
update those ranges when moving source code blocks.

On first visit, a browser language beginning with `zh` selects Chinese; all
others select English. The built-in language menu remembers a manual selection
in localStorage. Clear that site preference to return to automatic selection.
When storage is blocked, the menu uses a `lang` query parameter instead.

## GitHub Pages Deployment

In the repository's **Settings > Pages**, set **Source** to **GitHub Actions**.
The documentation workflow checks pull requests and deploys successful builds
from `main`. It can also be run manually from the Actions tab.

The configured URL is `https://covoyage.github.io/covonaut/`. If the repository
name or domain changes, update the VitePress `base`, sitemap hostname, and the
base path used by the link checker. The initial release does not configure a
custom domain or versioned documentation.

Deployment requires repository permissions and Pages availability. Repository
environment protection rules may require an approval for `github-pages`.