import { mkdtemp, readdir, readFile, mkdir, writeFile, rm } from 'node:fs/promises'
import { resolve, relative, dirname, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { parseHTML } from 'linkedom'

const output = resolve('.vitepress/dist')
const base = '/covonaut/'
const origin = 'https://docs.invalid'

async function walk(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const children = await Promise.all(entries.map(entry => entry.isDirectory()
    ? walk(join(directory, entry.name))
    : [join(directory, entry.name)]))
  return children.flat()
}

const files = await walk(output)
const available = new Set(files)
const pages = new Map()
for (const file of files.filter(file => file.endsWith('.html'))) {
  const { document } = parseHTML(await readFile(file, 'utf8'))
  pages.set(file, document)
}

const failures = []
const programs = new Map()
const pagePrograms = new Map()
const a2uiSnippets = new Map()
const workflowSnippets = new Map()
for (const [file, document] of pages) {
  const pageURL = new URL(base + relative(output, file).replaceAll('\\', '/'), origin)
  for (const element of document.querySelectorAll('a[href], link[href], script[src], img[src]')) {
    const value = element.getAttribute('href') ?? element.getAttribute('src')
    const target = new URL(value, pageURL)
    if (target.origin !== origin) continue
    if (!target.pathname.startsWith(base)) {
      failures.push(`${relative(output, file)}: outside base path: ${value}`)
      continue
    }
    const pathname = decodeURIComponent(target.pathname.slice(base.length))
    const path = resolve(output, pathname)
    const candidates = [path, `${path}.html`, join(path, 'index.html')]
    const found = candidates.find(candidate => available.has(candidate))
    if (!found) {
      failures.push(`${relative(output, file)}: missing target: ${value}`)
    } else if (target.hash && pages.has(found)) {
      const anchor = decodeURIComponent(target.hash.slice(1))
      if (!pages.get(found).getElementById(anchor)) {
        failures.push(`${relative(output, file)}: missing anchor: ${value}`)
      }
    }
  }
  for (const code of document.querySelectorAll('.vp-doc div[class*="language-go"] pre code')) {
    const source = code.textContent.trim()
    if (source.startsWith('package main')) {
      programs.set(source, file)
      if (!pagePrograms.has(file)) pagePrograms.set(file, new Set())
      pagePrograms.get(file).add(source)
    } else if (relative(output, file).replaceAll('\\', '/').endsWith('integrations/a2ui.html')) {
      if (!a2uiSnippets.has(file)) a2uiSnippets.set(file, [])
      a2uiSnippets.get(file).push(source)
    } else if (relative(output, file).replaceAll('\\', '/').endsWith('guide/workflows.html')) {
      if (!workflowSnippets.has(file)) workflowSnippets.set(file, [])
      workflowSnippets.get(file).push(source)
    }
  }
}

let translated = 0
for (const [file, document] of pages) {
  const path = relative(output, file).replaceAll('\\', '/')
  if (path === '404.html' || path.startsWith('zh/')) continue
  const counterpart = join(output, 'zh', path)
  const chinese = pages.get(counterpart)
  if (!chinese) {
    failures.push(`${path}: missing Chinese translation`)
    continue
  }
  translated++
  if (document.documentElement.lang !== 'en' || chinese.documentElement.lang !== 'zh-CN') {
    failures.push(`${path}: incorrect document language`)
  }
  for (const heading of document.querySelectorAll('.vp-doc h1[id], .vp-doc h2[id], .vp-doc h3[id]')) {
    if (!chinese.getElementById(heading.id)) {
      failures.push(`zh/${path}: missing corresponding heading: ${heading.id}`)
    }
  }
  for (const source of pagePrograms.get(file) ?? []) {
    if (!pagePrograms.get(counterpart)?.has(source)) {
      failures.push(`zh/${path}: complete Go example differs from English source`)
    }
  }
}
for (const file of pages.keys()) {
  const path = relative(output, file).replaceAll('\\', '/')
  if (path.startsWith('zh/') && !pages.has(join(output, path.slice(3)))) {
    failures.push(`${path}: missing English counterpart`)
  }
}

if (failures.length) throw new Error(failures.join('\n'))
console.log(`Checked internal links, assets, and anchors in ${pages.size} pages.`)
console.log(`Checked ${translated} bilingual page pairs, heading anchors, and complete Go examples.`)

const a2uiPage = join(output, 'integrations/a2ui.html')
const snippets = a2uiSnippets.get(a2uiPage) ?? []
const chineseSnippets = a2uiSnippets.get(join(output, 'zh/integrations/a2ui.html')) ?? []
if (snippets.length !== 4 || JSON.stringify(snippets) !== JSON.stringify(chineseSnippets)) {
  throw new Error('A2UI: expected four matching English and Chinese snippets')
}
const fixture = `envs := a2ui.NewSurface("profile", a2ui.BasicCatalogID).
  Add(a2ui.Column("root", "name"), a2ui.Text("name", a2ui.Bind("/user/name"))).
  Data("/user/name", "Ada Lovelace").Build()`
const a2uiProgram = `package main
import (
  "bytes"
  "errors"
  "fmt"
  "io"
  "log"
  "os"
  "github.com/covoyage/covonaut/a2a"
  "github.com/covoyage/covonaut/a2ui"
)
func consumeUI() error {
  ${fixture}
  var encoded bytes.Buffer
  if err := a2ui.NewEncoder(&encoded).EncodeAll(envs); err != nil { panic(err) }
  reader := &encoded
  if len(os.Args) > 1 && os.Args[1] == "empty" { encoded.Reset() }
  ${snippets[0]}
  return nil
}
func validateUI() error {
  ${fixture}
  store := a2ui.NewSurfaceStore()
  for _, env := range envs {
    if err := store.Apply(env); err != nil { panic(err) }
  }
  if len(os.Args) > 1 && os.Args[1] == "missing-profile" { store = a2ui.NewSurfaceStore() }
  ${snippets[1]}
  return nil
}
func bindA2A() error {
  ${fixture}
  ${snippets[2]}
  if len(decodedEnvs) != len(envs) { panic("A2A round trip lost envelopes") }
  return nil
}
func bindAGUI() error {
  env := a2ui.NewCreateSurface("profile", a2ui.BasicCatalogID)
  ${snippets[3]}
  return nil
}
func main() {
  if err := run(); err != nil { log.Fatal(err) }
}
func run() error {
  if len(os.Args) > 1 && os.Args[1] == "missing-profile" { return validateUI() }
  if err := consumeUI(); err != nil { return err }
  if len(os.Args) > 1 { return nil }
  if err := validateUI(); err != nil { return err }
  if err := bindA2A(); err != nil { return err }
  return bindAGUI()
}
`

const cache = resolve('.vitepress/cache')
const workflowPage = join(output, 'guide/workflows.html')
const workflowSources = (workflowSnippets.get(workflowPage) ?? []).slice(1)
const chineseWorkflowSources = (workflowSnippets.get(join(output, 'zh/guide/workflows.html')) ?? []).slice(1)
if (workflowSources.length !== 2 || JSON.stringify(workflowSources) !== JSON.stringify(chineseWorkflowSources)) {
  throw new Error('Workflows: expected two matching lifecycle snippets')
}
const workflowProgram = `package main
import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "github.com/covoyage/covonaut/agentcore"
  "github.com/covoyage/covonaut/workflow"
)
type echoProvider struct{}
func (echoProvider) Complete(_ context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
  return &agentcore.ProviderResponse{Content: req.Messages[len(req.Messages)-1].Content}, nil
}
func (echoProvider) Stream(context.Context, *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
  return nil, errors.New("streaming is not used")
}
type trackingExtension struct { closed int }
func (*trackingExtension) Name() string { return "lifecycle-check" }
func (*trackingExtension) Init(context.Context, *agentcore.Agent) error { return nil }
func (extension *trackingExtension) Dispose() error { extension.closed++; return nil }
func run() error {
  ctx := context.Background()
  tracked := &trackingExtension{}
  reviewConfig := agentcore.NewConfig(agentcore.WithModel("offline"), agentcore.WithProvider(echoProvider{}), agentcore.WithExtensions(tracked))
  writeConfig := agentcore.NewConfig(agentcore.WithModel("offline"), agentcore.WithProvider(echoProvider{}))
  ${workflowSources[0]}
  output, err := step.Run(ctx, "review")
  if err != nil { return err }
  if output != "review" || tracked.closed != 1 { return errors.New("step did not close its child") }
  ${workflowSources[1]}
  parent, err := agentcore.NewWithError(agentcore.NewConfig(agentcore.WithTools(delegate), agentcore.WithValidateArguments(true)))
  if err != nil { return err }
  defer parent.Close()
  result, err := parent.InvokeTool(ctx, "delegate", json.RawMessage("{\\"agent\\":\\"reviewer\\",\\"task\\":\\"review\\"}"))
  if err != nil { return err }
  if fmt.Sprint(result) != "review" || tracked.closed != 2 { return errors.New("delegate did not close its child") }
  return nil
}
func main() { if err := run(); err != nil { panic(err) } }
`
await mkdir(cache, { recursive: true })
const temporary = await mkdtemp(join(cache, 'snippets-'))
try {
  let index = 0
  for (const [source, page] of programs) {
    const directory = join(temporary, `program-${++index}`)
    await mkdir(directory)
    await writeFile(join(directory, 'main.go'), source + '\n')
    const result = spawnSync('go', ['test', directory], {
      cwd: dirname(resolve('package.json')), encoding: 'utf8'
    })
    if (result.error) throw result.error
    if (result.status !== 0) {
      throw new Error(`${relative(output, page)}: Go example failed\n${result.stdout}${result.stderr}`)
    }
  }
  console.log(`Compiled ${programs.size} distinct complete Go examples without provider calls.`)
  const directory = join(temporary, 'a2ui-snippets')
  await mkdir(directory)
  await writeFile(join(directory, 'main.go'), a2uiProgram)
  const result = spawnSync('go', ['run', directory], {
    cwd: dirname(resolve('package.json')), encoding: 'utf8'
  })
  if (result.error) throw result.error
  if (result.status !== 0) {
    throw new Error(`A2UI snippets failed\n${result.stdout}${result.stderr}`)
  }
  console.log('Compiled and ran four A2UI snippets with offline fixtures.')
  for (const scenario of ['empty', 'missing-profile']) {
    const result = spawnSync('go', ['run', directory, scenario], {
      cwd: dirname(resolve('package.json')), encoding: 'utf8'
    })
    if (result.error) throw result.error
    if (result.status === 0 || !result.stderr.includes('profile surface not found') || result.stderr.includes('panic:')) {
      throw new Error(`A2UI ${scenario}: expected a missing-surface error without panic\n${result.stdout}${result.stderr}`)
    }
  }
  console.log('Checked A2UI empty-input and missing-surface failures without panic.')
  const workflowDirectory = join(temporary, 'workflow-snippets')
  await mkdir(workflowDirectory)
  await writeFile(join(workflowDirectory, 'main.go'), workflowProgram)
  const workflowResult = spawnSync('go', ['run', workflowDirectory], {
    cwd: dirname(resolve('package.json')), encoding: 'utf8'
  })
  if (workflowResult.error) throw workflowResult.error
  if (workflowResult.status !== 0) {
    throw new Error(`Workflow snippets failed\n${workflowResult.stdout}${workflowResult.stderr}`)
  }
  console.log('Compiled and ran two workflow snippets; verified child agents were closed.')
} finally {
  await rm(temporary, { recursive: true, force: true })
}