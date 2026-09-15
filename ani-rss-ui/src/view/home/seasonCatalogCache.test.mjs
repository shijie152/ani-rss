import test from 'node:test'
import assert from 'node:assert/strict'
import {preserveSeasonOptions, readSeasonCache, writeSeasonCache} from './seasonCatalogCache.js'

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

test('does not reuse a non-empty cache with an incomplete response shape', () => {
  const storage = new MemoryStorage()
  const malformed = {
    version: 'v2',
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
  assert.deepEqual(readSeasonCache('mikan', '2026 秋', storage), {version: 'v2', savedAt: 1000, data})
})

test('keeps a valid filtered Mikan cache without repeated season options', () => {
  const storage = new MemoryStorage()
  const data = {seasons: [], weeks: [{weekLabel: '星期一', items: [{title: 'Demo', url: '/anime/1'}]}]}
  const savedAt = writeSeasonCache('mikan', '2026 春', data, storage, 1000)

  assert.equal(savedAt, 1000)
  assert.deepEqual(readSeasonCache('mikan', '2026 春', storage), {version: 'v2', savedAt: 1000, data})
})

test('preserves season options when a filtered response omits them', () => {
  const current = [{seasonLabel: '2026 夏'}]

  assert.deepEqual(preserveSeasonOptions(current, []), current)
  assert.deepEqual(preserveSeasonOptions(current, [{seasonLabel: '2026 春'}]), [{seasonLabel: '2026 春'}])
  assert.deepEqual(preserveSeasonOptions([], undefined), [])
})

test('keeps a valid AniBT catalogue cache', () => {
  const storage = new MemoryStorage()
  const data = {
    availableSeasons: ['2026 夏'],
    byWeekday: [{weekdayLabel: '周一', animes: [{bgmId: 42, title: {primary: 'Demo'}}]}]
  }
  const savedAt = writeSeasonCache('ani-bt', '2026 夏', data, storage, 1000)

  assert.equal(savedAt, 1000)
  assert.deepEqual(readSeasonCache('ani-bt', '2026 夏', storage), {version: 'v2', savedAt: 1000, data})
})
