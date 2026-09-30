import test from 'node:test'
import assert from 'node:assert/strict'
import {readdir, readFile} from 'node:fs/promises'
import {extname, join} from 'node:path'
import {fileURLToPath} from 'node:url'

const read = file => readFile(new URL(file, import.meta.url), 'utf8')

test('lazy loading contracts remain enabled for the performance paths', async () => {
  const router = await read('./router/index.js')
  const entry = await read('./main.js')
  const image = await read('./view/custom/LazyImage.vue')
  const season = await read('./view/home/SeasonCatalogView.vue')
  const subscription = await read('./view/home/SubscriptionListView.vue')
  const logs = await read('./view/home/LogsView.vue')

  assert.match(router, /import\('@\/view\/home\/DashboardView\.vue'\)/)
  assert.doesNotMatch(router, /import DashboardView from/)
  assert.doesNotMatch(entry, /import \* as ElementPlusIconsVue/)
  assert.match(image, /loading="lazy"/)
  assert.match(image, /decoding="async"/)
  assert.match(season, /<LazyImage/)
  assert.match(subscription, /content-visibility: auto/)
  assert.match(logs, /content-visibility: auto/)
})

const sourceFiles = async directory => {
  const entries = await readdir(directory, {withFileTypes: true})
  const nested = await Promise.all(entries.map(entry => {
    const path = join(directory, entry.name)
    return entry.isDirectory()
        ? sourceFiles(path)
        : ['.js', '.vue'].includes(extname(entry.name)) ? [path] : []
  }))
  return nested.flat()
}

const executableSource = (file, source) => file.endsWith('.vue')
    ? [...source.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)].map(match => match[1]).join('\n')
    : source

// 带凭据的 URL 不再扫源码校验：authenticatedUrl.test.mjs 直接断言 module 的
// 输出，比正则扫文本更强，也不再依赖「所有调用点都恰好写成某种形状」。
test('UI source does not hand-build authenticated query strings', async () => {
  const files = await sourceFiles(fileURLToPath(new URL('./', import.meta.url)))
  const findings = (await Promise.all(files.map(async file => ({
    file,
    source: executableSource(file, await readFile(file, 'utf8'))
  })))).flatMap(({file, source}) =>
    /[?&](s|api-key)=\$\{/.test(source) ? [file] : []
  )

  assert.deepEqual(findings, [])
})
