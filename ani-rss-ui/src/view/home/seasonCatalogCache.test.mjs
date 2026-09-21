import test from 'node:test'
import assert from 'node:assert/strict'
import {
  mikanSearchCacheKey,
  preserveSeasonOptions,
  readMikanSearchCache,
  readSeasonCache,
  resolveSeasonRequest,
  withSeasonOptions,
  writeMikanSearchCache,
  writeSeasonCache,
  writeSeasonCacheWithCurrentAlias
} from './seasonCatalogCache.js'

test('keeps requesting the source current season after it resolves a concrete label', () => {
  assert.equal(resolveSeasonRequest('2026 夏', true), 'current')
  assert.equal(resolveSeasonRequest('2025 冬', false), '2025 冬')
})

class MemoryStorage {
  #values = new Map()

  getItem(key) {
    return this.#values.get(key) ?? null
  }

  setItem(key, value) {
    this.#values.set(key, String(value))
  }
}

test('does not reuse an empty catalogue cache', () => {
  const storage = new MemoryStorage()
  const savedAt = writeSeasonCache('mikan', 'current', {seasons: [], weeks: []}, storage, 1000)

  assert.equal(savedAt, 0)
  assert.equal(readSeasonCache('mikan', 'current', storage), null)
})

test('invalidates the pre-versioned catalogue cache', () => {
  const storage = new MemoryStorage()
  storage.setItem('ani-rss:season-catalog:mikan:current', JSON.stringify({
    savedAt: 1000,
    data: {seasons: [], weeks: []}
  }))

  assert.equal(readSeasonCache('mikan', 'current', storage), null)
})

test('invalidates v2 catalogue cache after the season identity fix', () => {
  const storage = new MemoryStorage()
  const data = {seasons: [{seasonLabel: '2026 春'}], weeks: [{weekLabel: '星期一', items: [{title: 'Stale', url: '/stale'}]}]}
  storage.setItem('ani-rss:season-catalog:v3:mikan:2026%20%E6%98%A5', JSON.stringify({
    version: 'v2',
    savedAt: 1000,
    data
  }))

  assert.equal(readSeasonCache('mikan', '2026 春', storage), null)
})

test('does not reuse a non-empty cache with an incomplete response shape', () => {
  const storage = new MemoryStorage()
  const malformed = {
    version: 'v3',
    savedAt: 1000,
    data: {
      seasons: [],
      weeks: [{weekLabel: '星期一', items: [{title: '旧卡片'}]}]
    }
  }
  storage.setItem('ani-rss:season-catalog:v2:mikan:current', JSON.stringify(malformed))

  assert.equal(readSeasonCache('mikan', 'current', storage), null)
})

test('keeps a non-empty catalogue cache', () => {
  const storage = new MemoryStorage()
  const data = {seasons: [{seasonLabel: '2026 秋'}], weeks: [{weekLabel: '星期一', items: [{title: 'Demo', url: '/anime/1'}]}]}
  const savedAt = writeSeasonCache('mikan', '2026 秋', data, storage, 1000)

  assert.equal(savedAt, 1000)
  assert.deepEqual(readSeasonCache('mikan', '2026 秋', storage), {version: 'v3', savedAt: 1000, data})
})

test('keeps a valid filtered Mikan cache without repeated season options', () => {
  const storage = new MemoryStorage()
  const data = {seasons: [], weeks: [{weekLabel: '星期一', items: [{title: 'Demo', url: '/anime/1'}]}]}
  const savedAt = writeSeasonCache('mikan', '2026 春', data, storage, 1000)

  assert.equal(savedAt, 1000)
  assert.deepEqual(readSeasonCache('mikan', '2026 春', storage), {version: 'v3', savedAt: 1000, data})
})

test('updates the current-season cache alias after refreshing the current season', () => {
  const storage = new MemoryStorage()
  const oldData = {seasons: [{seasonLabel: '2026 秋'}], weeks: [{weekLabel: '星期一', items: [{title: 'Old', url: '/old'}]}]}
  const newData = {seasons: [{seasonLabel: '2026 秋'}], weeks: [{weekLabel: '星期一', items: [{title: 'New', url: '/new', exists: true}]}]}

  writeSeasonCache('mikan', 'current', oldData, storage, 1000)
  writeSeasonCacheWithCurrentAlias('mikan', '2026 秋', newData, storage, 2000)

  assert.deepEqual(readSeasonCache('mikan', 'current', storage), {
    version: 'v3',
    savedAt: 2000,
    data: newData
  })
})

test('does not let an old-season refresh overwrite the current-season cache alias', () => {
  const storage = new MemoryStorage()
  const current = {seasons: [{seasonLabel: '2026 秋'}], weeks: [{weekLabel: '星期一', items: [{title: 'Current', url: '/current'}]}]}
  const old = {seasons: [{seasonLabel: '2025 冬'}], weeks: [{weekLabel: '星期一', items: [{title: 'Old', url: '/old'}]}]}

  writeSeasonCache('mikan', 'current', current, storage, 1000)
  writeSeasonCacheWithCurrentAlias('mikan', '2025 冬', old, storage, 2000, false)

  assert.equal(readSeasonCache('mikan', 'current', storage).data.weeks[0].items[0].title, 'Current')
})

test('preserves season options when a filtered response omits them', () => {
  const current = [{seasonLabel: '2026 夏'}]

  assert.deepEqual(preserveSeasonOptions(current, []), current)
  assert.deepEqual(preserveSeasonOptions(current, [{seasonLabel: '2026 春'}]), [{seasonLabel: '2026 春'}])
  assert.deepEqual(preserveSeasonOptions([], undefined), [])
})

test('persists season options alongside a filtered response', () => {
  const data = {seasons: [], weeks: [{weekLabel: '星期一', items: [{title: 'Demo', url: '/anime/1'}]}]}
  const options = [{seasonLabel: '2026 春'}]

  assert.deepEqual(withSeasonOptions('mikan', data, options), {...data, seasons: options})
  assert.deepEqual(withSeasonOptions('ani-bt', {availableSeasons: [], byWeekday: []}, ['2026 春']), {
    availableSeasons: ['2026 春'],
    byWeekday: []
  })
})

test('keeps a valid AniBT catalogue cache', () => {
  const storage = new MemoryStorage()
  const data = {
    availableSeasons: ['2026 夏'],
    byWeekday: [{weekdayLabel: '周一', animes: [{bgmId: 42, title: {primary: 'Demo'}}]}]
  }
  const savedAt = writeSeasonCache('ani-bt', '2026 夏', data, storage, 1000)

  assert.equal(savedAt, 1000)
  assert.deepEqual(readSeasonCache('ani-bt', '2026 夏', storage), {version: 'v3', savedAt: 1000, data})
})

test('normalizes Mikan search cache keys', () => {
  assert.equal(mikanSearchCacheKey('  Bocchi  '), mikanSearchCacheKey('bocchi'))
})

test('keeps a valid Mikan search cache, including empty results', () => {
  const storage = new MemoryStorage()
  const data = {seasons: [], weeks: [{weekLabel: 'Search', items: []}], totalItems: 0}
  const savedAt = writeMikanSearchCache('demo', data, storage, 1000)

  assert.equal(savedAt, 1000)
  assert.deepEqual(readMikanSearchCache('demo', storage), {version: 'v3', savedAt: 1000, data})
})

test('does not reuse a malformed Mikan search cache', () => {
  const storage = new MemoryStorage()
  storage.setItem(mikanSearchCacheKey('demo'), JSON.stringify({
    version: 'v3',
    savedAt: 1000,
    data: {weeks: [{weekLabel: 'Search', items: []}]}
  }))

  assert.equal(readMikanSearchCache('demo', storage), null)
})
