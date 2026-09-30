import test from 'node:test'
import assert from 'node:assert/strict'
import {createCatalogRequest} from './catalogRequest.js'

const ttl = 7 * 24 * 60 * 60 * 1000

// 内存缓存 port，形状与 seasonCatalogCache 的 {savedAt, data} 一致。
const memoryCache = () => {
  const values = new Map()
  let clock = 1000
  return {
    read: key => values.get(key) ?? null,
    write: (key, data) => {
      const savedAt = (clock += 1)
      values.set(key, {savedAt, data})
      return savedAt
    }
  }
}

test('fresh cache answers without touching the network', async () => {
  const cache = memoryCache()
  const catalog = createCatalogRequest({ttl, now: () => 1000})
  let calls = 0
  const fetch = async () => { calls++; return {seasons: []} }

  await catalog.load({key: 'season', ...cache, fetch})
  const result = await catalog.load({key: 'season', ...cache, fetch})

  assert.equal(calls, 1)
  assert.deepEqual(result.data, {seasons: []})
  assert.equal(result.source, 'cache')
})

test('stale cache is served while the refresh runs in the background', async () => {
  let now = 1000
  const cache = memoryCache()
  // 注入同步 executor：刷新在 load 返回前完成，断言无需等真实后台任务。
  const catalog = createCatalogRequest({ttl, now: () => now, background: task => { void task() }})
  await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'old'})})

  now += ttl + 1
  const stale = await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'new'})})

  assert.equal(stale.source, 'stale')
  assert.deepEqual(stale.data, {value: 'old'})

  const refreshed = await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'never'})})
  assert.deepEqual(refreshed.data, {value: 'new'})
})

test('stale read returns before the refresh finishes', async () => {
  let now = 1000
  const cache = memoryCache()
  let finishRefresh
  // 真实异步 executor：刷新尚未完成时 load 就必须返回旧值。
  const catalog = createCatalogRequest({
    ttl,
    now: () => now,
    background: task => { finishRefresh = task }
  })
  await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'old'})})

  now += ttl + 1
  const stale = await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'new'})})
  assert.equal(stale.source, 'stale')
  assert.deepEqual(stale.data, {value: 'old'})
  assert.equal(typeof finishRefresh, 'function', 'refresh must be handed to the executor')

  await finishRefresh()
  const refreshed = await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'never'})})
  assert.deepEqual(refreshed.data, {value: 'new'})
})

test('failed request falls back to the cached catalogue', async () => {
  const cache = memoryCache()
  const catalog = createCatalogRequest({ttl, now: () => 1000})
  await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'cached'})})

  const result = await catalog.load({
    key: 'season',
    ...cache,
    force: true,
    fetch: async () => { throw new Error('上游不可用') }
  })

  assert.deepEqual(result.data, {value: 'cached'})
  assert.equal(result.source, 'cache')
  assert.equal(result.error.message, '上游不可用')
})

// 被取代的响应必须带可判定的标记：视图靠它丢弃旧数据。此前用 {stale:true}
// 而视图检查 source === 'stale'，标记对不上，旧响应照旧被渲染。
test('superseded responses are marked so callers can drop them', async () => {
  const catalog = createCatalogRequest({ttl: 1000, now: () => 1})
  let resolveSlow
  const slow = catalog.load({key: 'A', fetch: () => new Promise(resolve => { resolveSlow = resolve })})
  const fast = catalog.load({key: 'B', fetch: async () => ({value: 'B'})})
  resolveSlow({value: 'A'})

  const superseded = await slow
  assert.equal(superseded.stale, true)
  assert.equal(superseded.source, undefined)
  assert.deepEqual((await fast).data, {value: 'B'})
})

test('a late response from an older request is discarded', async () => {
  const cache = memoryCache()
  const catalog = createCatalogRequest({ttl, now: () => 1000})
  let resolveFirst
  const first = catalog.load({
    key: 'season',
    ...cache,
    fetch: () => new Promise(resolve => { resolveFirst = resolve })
  })
  const second = catalog.load({key: 'season', ...cache, force: true, fetch: async () => ({value: 'second'})})

  resolveFirst({value: 'first'})
  const [firstResult, secondResult] = await Promise.all([first, second])

  assert.equal(secondResult.data.value, 'second')
  assert.equal(firstResult.stale, true)
  const settled = await catalog.load({key: 'season', ...cache, fetch: async () => ({value: 'never'})})
  assert.equal(settled.data.value, 'second')
})

// Mikan 搜索与季度目录共用一个 module，但新鲜窗口不同（搜索 30 分钟、季度 7 天）：
// 允许按调用覆盖 ttl，否则只能再手写一套决策。
test('a call can override the freshness window', async () => {
  const cache = memoryCache()
  const catalog = createCatalogRequest({ttl: 7 * 24 * 60 * 60 * 1000, now: () => 1000})
  let calls = 0
  const fetch = async () => { calls++; return {value: calls} }

  await catalog.load({key: 'search', ...cache, fetch, ttl: 60 * 1000})
  const cached = await catalog.load({key: 'search', ...cache, fetch, ttl: 60 * 1000})

  assert.equal(calls, 1)
  assert.equal(cached.source, 'cache')
})

test('a shorter per-call window makes the entry stale while the default would not', async () => {
  const cache = memoryCache()
  let now = 1000
  const catalog = createCatalogRequest({ttl: 7 * 24 * 60 * 60 * 1000, now: () => now})
  const fetch = async () => ({value: 'fresh'})
  await catalog.load({key: 'k', ...cache, fetch, ttl: 60 * 1000})

  now += 2 * 60 * 1000
  const result = await catalog.load({key: 'k', ...cache, fetch, ttl: 60 * 1000})

  assert.equal(result.source, 'stale')
})

test('without a cache port the module still loads', async () => {
  const catalog = createCatalogRequest({ttl, now: () => 1000})
  const result = await catalog.load({key: 'season', fetch: async () => ({value: 'direct'})})
  assert.deepEqual(result.data, {value: 'direct'})
})
