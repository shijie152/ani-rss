import test from 'node:test'
import assert from 'node:assert/strict'
import {createMikanCacheScheduler} from './mikanCacheScheduler.js'

test('fuzz: multiple Mikan views schedule one shared refresh', async () => {
  const timers = []
  const scheduler = createMikanCacheScheduler({
    now: () => new Date('2026-09-25T02:00:00Z'),
    scheduleTimer: (callback, delay) => {
      const timer = {callback, delay, cancelled: false}
      timers.push(timer)
      return timer
    },
    cancelTimer: timer => { timer.cancelled = true }
  })
  let refreshes = 0
  const remove = Array.from({length: 100}, (_, index) => scheduler.register(index, async () => {
    refreshes++
  }))

  assert.equal(timers.length, 1)
  assert.ok(timers[0].delay > 0)
  await timers[0].callback()
  assert.equal(refreshes, 1)
  remove.forEach(unregister => unregister())
})

test('removing the last Mikan view cancels the pending refresh', () => {
  const timers = []
  const scheduler = createMikanCacheScheduler({
    now: () => new Date('2026-09-25T02:00:00Z'),
    scheduleTimer: callback => {
      const timer = {callback, cancelled: false}
      timers.push(timer)
      return timer
    },
    cancelTimer: timer => { timer.cancelled = true }
  })
  const remove = scheduler.register('only', () => {})
  remove()
  assert.equal(timers[0].cancelled, true)
})
