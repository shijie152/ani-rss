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

test('without a cache port the module still loads', async () => {
  const catalog = createCatalogRequest({ttl, now: () => 1000})
  const result = await catalog.load({key: 'season', fetch: async () => ({value: 'direct'})})
  assert.deepEqual(result.data, {value: 'direct'})
})
