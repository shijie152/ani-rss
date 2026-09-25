import test from 'node:test'
import assert from 'node:assert/strict'
import {notifySubscriptionsChanged, onSubscriptionsChanged} from './subscriptionChanges.js'

test('listeners receive one notification and can unsubscribe independently', () => {
  let first = 0
  let second = 0
  const removeFirst = onSubscriptionsChanged(() => first++)
  onSubscriptionsChanged(() => second++)
  notifySubscriptionsChanged()
  removeFirst()
  notifySubscriptionsChanged()
  assert.equal(first, 1)
  assert.equal(second, 2)
})

test('a listener may safely remove itself during notification', () => {
  let count = 0
  let remove
  remove = onSubscriptionsChanged(() => {
    count++
    remove()
  })
  notifySubscriptionsChanged()
  notifySubscriptionsChanged()
  assert.equal(count, 1)
})
