import test from 'node:test'
import assert from 'node:assert/strict'
import {createPollingController} from './pollingController.js'

const clock = () => {
  const timers = []
  return {
    timers,
    schedule(callback, delay) {
      const timer = {callback, delay, cancelled: false}
      timers.push(timer)
      return timer
    },
    cancel(timer) { timer.cancelled = true },
    async runNext() {
      const timer = timers.find(item => !item.cancelled && !item.ran)
      if (!timer) return
      timer.ran = true
      await timer.callback()
    }
  }
}

test('serial polling does not overlap a pending task', async () => {
  const fake = clock()
  let calls = 0
  let release
  const task = () => {
    calls++
    return new Promise(resolve => { release = resolve })
  }
  const controller = createPollingController(task, 5000, {
    scheduleTimer: fake.schedule,
    cancelTimer: fake.cancel,
    isHidden: () => false
  })

  controller.start()
  controller.start()
  assert.equal(calls, 1)
  assert.equal(fake.timers.length, 0)
  release()
  await Promise.resolve()
  assert.equal(fake.timers.length, 1)
})

test('polling stops scheduling when hidden or explicitly stopped', async () => {
  const fake = clock()
  let hidden = true
  let calls = 0
  const controller = createPollingController(() => { calls++ }, 5000, {
    immediate: false,
    scheduleTimer: fake.schedule,
    cancelTimer: fake.cancel,
    isHidden: () => hidden
  })

  controller.start()
  assert.equal(fake.timers.length, 0)
  hidden = false
  controller.run()
  assert.equal(calls, 1)
  controller.stop()
  assert.equal(fake.timers.every(timer => timer.cancelled || timer.ran), true)
})

test('paused polling resumes only when it was active before the page was hidden', async () => {
  const fake = clock()
  let hidden = false
  let calls = 0
  const controller = createPollingController(() => { calls++ }, 5000, {
    immediate: false,
    scheduleTimer: fake.schedule,
    cancelTimer: fake.cancel,
    isHidden: () => hidden
  })
  controller.start()
  hidden = true
  controller.pause()
  hidden = false
  controller.resume()
  assert.equal(fake.timers.length, 2)
  controller.stop()
  controller.resume()
  assert.equal(calls, 0)
})
