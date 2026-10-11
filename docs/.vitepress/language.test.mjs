import assert from 'node:assert/strict'
import { test } from 'node:test'
import { runInNewContext } from 'node:vm'
import { languageScript, localizedPath, selectLanguage } from './language.mjs'

const base = '/covonaut/'

test('Chinese browser variants select Chinese; other languages select English', () => {
  for (const language of ['zh', 'zh-CN', 'zh-TW', 'zh-Hant', 'ZH-hk']) {
    assert.equal(selectLanguage(null, language), 'zh')
  }
  for (const language of ['en', 'en-US', 'ja-JP', 'fr', undefined]) {
    assert.equal(selectLanguage(null, language), 'en')
  }
  assert.equal(selectLanguage('en', 'zh-CN'), 'en')
  assert.equal(selectLanguage('zh', 'en-US'), 'zh')
  assert.equal(selectLanguage('invalid', 'zh-CN'), 'zh')
})

test('localized paths retain the current page and deployment base', () => {
  assert.equal(localizedPath(base, 'zh', base), base + 'zh/')
  assert.equal(localizedPath(base + 'guide/tools', 'zh', base), base + 'zh/guide/tools')
  assert.equal(localizedPath(base + 'zh/guide/tools', 'en', base), base + 'guide/tools')
  assert.equal(localizedPath(base + 'zh/', 'en', base), base)
  assert.equal(localizedPath(base + 'zh/guide/tools', 'zh', base), base + 'zh/guide/tools')
  assert.equal(localizedPath('/other/site', 'zh', base), '/other/site')
})

function runScript(browserLanguage, preference, path, blocked = false) {
  const url = new URL(path, 'https://docs.example')
  const state = { redirect: null, navigation: null, preference, listener: null }
  runInNewContext(languageScript(base), {
    URL,
    navigator: { language: browserLanguage },
    location: {
      href: url.href, origin: url.origin, pathname: url.pathname,
      search: url.search, hash: url.hash,
      replace: path => { state.redirect = path },
      assign: url => { state.navigation = url }
    },
    localStorage: {
      getItem: () => { if (blocked) throw new Error('blocked'); return preference },
      setItem: (_, language) => { if (blocked) throw new Error('blocked'); state.preference = language }
    },
    document: { addEventListener: (_, listener) => { state.listener = listener } }
  })
  return state
}

test('early redirect preserves query and fragment and does not loop', () => {
  assert.equal(runScript('zh-CN', null, base + 'guide/tools?from=test#hooks').redirect,
    base + 'zh/guide/tools?from=test#hooks')
  assert.equal(runScript('zh-CN', null, base + 'zh/guide/tools').redirect, null)
  assert.equal(runScript('en-US', null, base + 'zh/guide/tools').redirect, base + 'guide/tools')
  assert.equal(runScript('zh-CN', 'en', base + 'guide/tools').redirect, null)
  assert.equal(runScript('en-US', null, base + 'zh/guide/tools?lang=zh').redirect, null)
})

test('manual menu selection persists and remains usable with blocked storage', () => {
  for (const blocked of [false, true]) {
    const state = runScript('zh-CN', null, base + 'zh/guide/tools', blocked)
    const link = { href: 'https://docs.example' + base + 'guide/tools#hooks', closest: () => true }
    let prevented = false
    state.listener({
      target: { closest: () => link }, button: 0,
      preventDefault: () => { prevented = true },
      stopImmediatePropagation: () => {}
    })
    if (blocked) {
      assert.equal(new URL(link.href).searchParams.get('lang'), 'en')
      assert.equal(state.navigation, link.href)
      assert.equal(prevented, true)
    } else {
      assert.equal(state.preference, 'en')
      assert.equal(state.navigation, null)
      assert.equal(prevented, false)
    }
  }
})

test('storage fallback does not intercept modified clicks or new-tab links', () => {
  for (const extra of [{ ctrlKey: true }, { metaKey: true }, { button: 1 }]) {
    const state = runScript('zh-CN', null, base + 'zh/guide/tools', true)
    const link = { href: 'https://docs.example' + base + 'guide/tools', closest: () => true }
    state.listener({ target: { closest: () => link }, button: 0, ...extra })
    assert.equal(new URL(link.href).searchParams.get('lang'), 'en')
    assert.equal(state.navigation, null)
  }
})