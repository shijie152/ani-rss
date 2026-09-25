import test from 'node:test'
import assert from 'node:assert/strict'
import {createDailyRefreshRegistry, nextDailyRefreshDelay} from './dailyScheduler.js'

test('daily refresh is scheduled for the next 03:00', () => {
  const before = nextDailyRefreshDelay(() => new Date('2026-09-25T02:00:00'))
  const after = nextDailyRefreshDelay(() => new Date('2026-09-25T04:00:00'))
  assert.equal(before, 60 * 60 * 1000)
  assert.equal(after, 23 * 60 * 60 * 1000)
})

test('daily refresh registry keeps one timer and cancels it after the last owner leaves', () => {
  const timers = []
  const scheduler = createDailyRefreshRegistry({
    now: () => new Date('2026-09-25T02:00:00'),
    scheduleTimer: (callback, delay) => {
      const timer = {callback, delay, cancelled: false}
      timers.push(timer)
      return timer
    },
    cancelTimer: timer => { timer.cancelled = true }
  })

  const removes = [
    scheduler.register('one', () => {}),
    scheduler.register('two', () => {}),
    scheduler.register('three', () => {})
  ]
  assert.equal(timers.length, 1)
  removes.forEach(remove => remove())
  assert.equal(timers[0].cancelled, true)
})

test('Mikan and season owners share one timer and each group refreshes once', async () => {
  const timers = []
  const scheduler = createDailyRefreshRegistry({
    now: () => new Date('2026-09-25T02:00:00'),
    scheduleTimer: (callback, delay) => {
      const timer = {callback, delay, cancelled: false}
      timers.push(timer)
      return timer
    },
    cancelTimer: timer => { timer.cancelled = true }
  })
  let mikanRefreshes = 0
  let seasonRefreshes = 0

  const unregisterMikan = scheduler.register('mikan-view', () => { mikanRefreshes++ }, 'mikan')
  const unregisterSeason = scheduler.register('season-page', () => { seasonRefreshes++ }, 'season')

  assert.equal(timers.length, 1)
  await timers[0].callback()
  assert.equal(mikanRefreshes, 1)
  assert.equal(seasonRefreshes, 1)
  assert.equal(timers.length, 2)

  unregisterMikan()
  unregisterSeason()
  assert.equal(timers[1].cancelled, true)
})

test('daily scheduler waits for an async refresh before scheduling again', async () => {
  const timers = []
  let release
  let refreshes = 0
  const scheduler = createDailyRefreshRegistry({
    now: () => new Date('2026-09-25T02:00:00'),
    scheduleTimer: callback => {
      const timer = {callback, cancelled: false}
      timers.push(timer)
      return timer
    },
    cancelTimer: timer => { timer.cancelled = true }
  })

  scheduler.register('one', () => new Promise(resolve => {
    refreshes++
    release = resolve
  }))
  const refreshPromise = timers[0].callback()
  await Promise.resolve()
  assert.equal(refreshes, 1)
  assert.equal(timers.length, 1)
  release()
  await refreshPromise
  assert.equal(timers.length, 2)
})
