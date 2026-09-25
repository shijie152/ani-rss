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
  let mutant = mutate(original)
  assert.notEqual(mutant, original, `${label}: mutation did not change source`)
  const target = join(tempDir, `${label.replaceAll(/[^a-z0-9_-]/gi, '_')}.mjs`)
  await writeFile(target, mutant)
  return import(`${pathToFileURL(target).href}?mutation=${Date.now()}-${Math.random()}`)
}

const kill = async (label, relativePath, mutate, assertion) => {
  const module = await loadMutant(relativePath, label, mutate)
  try {
    await assertion(module)
  } catch (_) {
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
    "return method === 'POST' && readOnlyPostPaths.has(url.split('?')[0])",
    "return method === 'POST' || readOnlyPostPaths.has(url.split('?')[0])"
  ),
  ({shouldDedupe}) => assert.equal(shouldDedupe('api/addAni', 'POST'), false)
)

await kill(
  'request-key-body',
  'src/js/requestUtils.js',
  source => source.replace('return `${method}:${url}:${serialized}`', 'return `${method}:${url}`'),
  ({requestKey}) => assert.notEqual(
    requestKey('api/mikan', 'POST', {season: '2026 春'}),
    requestKey('api/mikan', 'POST', {season: '2026 夏'})
  )
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

console.log('UI mutation tests passed: 8 mutants killed')
