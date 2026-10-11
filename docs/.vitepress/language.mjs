export function selectLanguage(preference, browserLanguage) {
  if (preference === 'zh' || preference === 'en') return preference
  return browserLanguage?.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function localizedPath(pathname, language, base) {
  if (!pathname.startsWith(base)) return pathname
  let path = pathname.slice(base.length)
  if (path === 'zh') path = ''
  else if (path.startsWith('zh/')) path = path.slice(3)
  return base + (language === 'zh' ? 'zh/' : '') + path
}

export function languageScript(base) {
  return `(() => {
    const base = ${JSON.stringify(base)};
    const selectLanguage = ${selectLanguage.toString()};
    const localizedPath = ${localizedPath.toString()};
    const key = 'covonaut-docs-language';
    let preference;
    try { preference = localStorage.getItem(key); } catch {}
    const explicit = new URL(location.href).searchParams.get('lang');
    if (explicit === 'zh' || explicit === 'en') preference = explicit;
    const language = selectLanguage(preference, navigator.language || navigator.languages?.[0]);
    const path = localizedPath(location.pathname, language, base);
    if (path !== location.pathname) location.replace(path + location.search + location.hash);
    document.addEventListener('click', event => {
      const link = event.target?.closest?.('a');
      if (!link?.closest('.VPNavBarTranslations, .VPNavScreenTranslations')) return;
      const target = new URL(link.href, location.href);
      if (target.origin !== location.origin || !target.pathname.startsWith(base)) return;
      const language = target.pathname.startsWith(base + 'zh/') ? 'zh' : 'en';
      try { localStorage.setItem(key, language); } catch {
        target.searchParams.set('lang', language);
        link.href = target.href;
        if (event.button === 0 && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey && !link.target) {
          event.preventDefault();
          event.stopImmediatePropagation();
          location.assign(target.href);
        }
      }
    }, true);
  })();`
}