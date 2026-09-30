import assert from 'node:assert/strict'
import {mkdir, readFile, writeFile} from 'node:fs/promises'
import {join, resolve} from 'node:path'
import {pathToFileURL} from 'node:url'

const repo = resolve(new URL('..', import.meta.url).pathname)
const tempDir = join(repo, '.scratch', '.tmp', 'ui-mutations')
await mkdir(tempDir, {recursive: true})

const loadMutant = async (relativePath, label, mutate) => {
  const originalPath = join(repo, 'ani-rss-ui', relativePath)
  const original = await readFile(originalPath, 'utf8')
  // 变异体写到临时目录后，相对 import 不再指向源码树；逐条改回绝对路径。
  let mutant = mutate(original).replaceAll(
    /from\s+'(\.\/[^']+)'/g,
    (_, specifier) => `from '${pathToFileURL(join(repo, 'ani-rss-ui/src/js', specifier)).href}'`
  )
  assert.notEqual(mutant, original, `${label}: mutation did not change source`)
  const target = join(tempDir, `${label.replaceAll(/[^a-z0-9_-]/gi, '_')}.mjs`)
  await writeFile(target, mutant)
  return import(`${pathToFileURL(target).href}?mutation=${Date.now()}-${Math.random()}`)
}

let killed = 0
const kill = async (label, relativePath, mutate, assertion) => {
  // 先确认断言在未变异的源码上通过：否则断言写错也会「杀死」变异，
  // 变异测试就永远绿、毫无约束力。
  const pristine = await import(
    `${pathToFileURL(join(repo, 'ani-rss-ui', relativePath)).href}?pristine=${Date.now()}-${Math.random()}`
  )
  try {
    await assertion(pristine)
  } catch (error) {
    throw new Error(`${label}: assertion fails on unmutated source (${error?.message ?? error})`)
  }
  const module = await loadMutant(relativePath, label, mutate)
  try {
    await assertion(module)
  } catch (_) {
    killed++
    console.log(`KILLED ${label}`)
    return
  }
  throw new Error(`SURVIVED ${label}`)
}

await kill(
  'request-query-encoding',
  'src/js/requestUtils.js',
  source => source.replace('parsed.searchParams.set(key, String(value))', 'void value'),
  ({withQuery}) => {
    const parsed = new URL(withQuery('api/search', {query: 'a&b#中文'}), 'http://localhost/')
    assert.equal(parsed.searchParams.get('query'), 'a&b#中文')
  }
)

await kill(
  'request-dedupe-scope',
  'src/js/requestUtils.js',
  source => source.replace(
    'method === \'POST\' && readOnlyPaths',
    'method === \'POST\' || readOnlyPaths'
  ),
  ({shouldDedupe}) => assert.equal(shouldDedupe('api/addAni', 'POST'), false)
)

await kill(
  'request-key-body',
  'src/js/requestUtils.js',
  source => source.replace(':${serialized}`', '`'),
  ({requestKey}) => assert.notEqual(
    requestKey('api/mikan', 'POST', {season: '2026 春'}),
    requestKey('api/mikan', 'POST', {season: '2026 夏'})
  )
)

await kill(
  'request-key-keeps-query',
  'src/js/requestUtils.js',
  source => source.replace('${url.replace(/^\\/+/, \'\')}', '${endpointPath(url)}'),
  ({requestKey}) => assert.notEqual(
    requestKey('api/mikan?text=春', 'POST'),
    requestKey('api/mikan?text=夏', 'POST')
  )
)

// 复制助手：Clipboard 不可用时必须回退，且失败要如实返回 false。
await kill(
  'copy-text-fallback-and-result',
  'src/js/sourceBrowsing.js',
  source => source.replace('return copied', 'return true'),
  async ({copyText}) => {
    const document = {
      body: {appendChild() {}, removeChild() {}},
      createElement: () => ({value: '', select() {}}),
      execCommand: () => false
    }
    assert.equal(await copyText('rss', null, {document}), false)
  }
)

// 鉴权 URL module：凭据必须进 query，且两种凭据不能互相串味。
await kill(
  'authenticated-url-credential-placement',
  'src/js/authenticatedUrl.js',
  source => source.replace('build(path, {...params, s: token}, base)', 'build(path, {...params}, base)'),
  ({sessionUrl}) => {
    const url = new URL(sessionUrl('api/downloadLogs', {}, {base: 'http://ani-rss.test/', token: 'tok'}))
    assert.equal(url.searchParams.get('s'), 'tok')
  }
)

// 源站浏览 module：同一番剧的字幕组条目必须归到一组（否则批量添加会重复建订阅）。
await kill(
  'source-browsing-groups-by-subject',
  'src/js/sourceBrowsing.js',
  source => source.replace('const subject = adapter.subjectId(item)', 'const subject = item.rss'),
  async ({submitBatch, sourceAdapters}) => {
    // 用真实 adapter，而不是手造一个残缺对象（缺字段会让断言在未变异源码上就失败）。
    const adapter = sourceAdapters.mikan
    const drafts = []
    await submitBatch([
      // 两条 rss 不同、但 bangumiId 相同：只有真按 subject 分组才会合并成一条。
      {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1&group=a', label: '字幕组A'},
      {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1&group=b', label: '字幕组B'}
    ], adapter, {add: async draft => drafts.push(draft)})
    assert.equal(drafts.length, 1)
  }
)

// 目录请求 module 的两条核心决策：过期必须后台刷新，旧响应必须丢弃。
await kill(
  'catalog-stale-serves-and-refreshes',
  'src/js/catalogRequest.js',
  source => source.replace('if (cached && !force) {', 'if (false) {'),
  async ({createCatalogRequest}) => {
    let now = 1000
    const values = new Map()
    const cache = {
      read: key => values.get(key) ?? null,
      write: (key, data) => { values.set(key, {savedAt: (now += 1), data}); return now }
    }
    // 注入同步 executor：刷新在 load 返回前完成，断言不依赖任何测试专用 API。
    const catalog = createCatalogRequest({ttl: 100, now: () => now, background: task => { void task() }})
    await catalog.load({key: 'k', ...cache, fetch: async () => ({value: 'old'})})
    now += 1000
    const result = await catalog.load({key: 'k', ...cache, fetch: async () => ({value: 'new'})})
    assert.equal(result.source, 'stale')
    assert.equal(result.data.value, 'old')
    const refreshed = await catalog.load({key: 'k', ...cache, fetch: async () => ({value: 'never'})})
    assert.equal(refreshed.data.value, 'new')
  }
)

// 端点表是去重决策的唯一来源：把它改空（等价于回到「两份手写清单漂移」的
// 老样子）必须被测试抓住。
await kill(
  'endpoint-readonly-table-drift',
  'src/js/endpoints.js',
  source => source.replace(
    'Object.values(endpoints).filter(item => item.readOnly).map(item => item.path)',
    '[]'
  ),
  ({readOnlyPaths}) => assert.equal(readOnlyPaths.has('api/mikan'), true)
)

await kill(
  'polling-no-overlap',
  'src/js/pollingController.js',
  source => source.replace('inFlight || isHidden()', 'isHidden()'),
  async ({createPollingController}) => {
    let calls = 0
    let release
    const controller = createPollingController(() => {
      calls++
      return new Promise(resolve => { release = resolve })
    }, 1000, {scheduleTimer: () => ({}), cancelTimer: () => {}, isHidden: () => false})
    controller.start()
    controller.run()
    assert.equal(calls, 1)
    release()
    controller.stop()
  }
)

await kill(
  'polling-hidden-stop',
  'src/js/pollingController.js',
  source => source.replace('if (!running || !wanted || inFlight || isHidden()) return', 'if (!running || !wanted || inFlight) return'),
  async ({createPollingController}) => {
    let calls = 0
    const controller = createPollingController(() => { calls++ }, 1000, {
      isHidden: () => true,
      scheduleTimer: () => ({}),
      cancelTimer: () => {}
    })
    controller.start()
    await Promise.resolve()
    assert.equal(calls, 0)
  }
)

await kill(
  'mikan-single-timer',
  'src/js/dailyScheduler.js',
  source => source.replace('if (timer !== undefined || listeners.size === 0) return', 'if (listeners.size === 0) return'),
  ({createDailyRefreshRegistry}) => {
    const timers = []
    const scheduler = createDailyRefreshRegistry({
      now: () => new Date('2026-09-25T02:00:00Z'),
      scheduleTimer: callback => { const timer = {callback}; timers.push(timer); return timer },
      cancelTimer: () => {}
    })
    scheduler.register('one', () => {})
    scheduler.register('two', () => {})
    assert.equal(timers.length, 1)
  }
)

await kill(
  'mikan-cancel-timer',
  'src/js/dailyScheduler.js',
  source => source.replace('cancelTimer(timer)', 'void timer'),
  ({createDailyRefreshRegistry}) => {
    const timers = []
    const scheduler = createDailyRefreshRegistry({
      now: () => new Date('2026-09-25T02:00:00Z'),
      scheduleTimer: callback => { const timer = {callback, cancelled: false}; timers.push(timer); return timer },
      cancelTimer: timer => { timer.cancelled = true }
    })
    const remove = scheduler.register('one', () => {})
    remove()
    assert.equal(timers[0].cancelled, true)
  }
)

await kill(
  'daily-refresh-each-owner-group',
  'src/js/dailyScheduler.js',
  source => source.replace('const refreshes = [...listeners.values()]', 'const refreshes = [...listeners.values()].slice(0, 1)'),
  async ({createDailyRefreshRegistry}) => {
    let scheduled
    const scheduler = createDailyRefreshRegistry({
      now: () => new Date('2026-09-25T02:00:00Z'),
      scheduleTimer: callback => { scheduled = callback; return callback },
      cancelTimer: () => {}
    })
    let refreshes = 0
    scheduler.register('mikan', () => { refreshes++ }, 'mikan')
    scheduler.register('season', () => { refreshes++ }, 'season')
    await scheduled()
    assert.equal(refreshes, 2)
  }
)

console.log(`UI mutation tests passed: ${killed} mutants killed`)

// 文档里的「N 个变异」是手写的，历史上漂移过两次；这里核对一次，
// 数字对不上就让门禁失败，而不是等下次有人偶然发现。
const docPath = join(repo, 'docs', 'testing-ui.md')
const doc = await readFile(docPath, 'utf8')
const documented = doc.match(/等 (\d+) 个行为/)?.[1]
if (documented !== String(killed)) {
  throw new Error(`docs/testing-ui.md 写的是 ${documented ?? '(未找到)'} 个变异，实际杀死 ${killed} 个`)
}
