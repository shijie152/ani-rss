import test from 'node:test'
import assert from 'node:assert/strict'
import {requestKey, shouldDedupe, withQuery} from './requestUtils.js'

const random = seed => {
  let value = seed >>> 0
  return () => {
    value = (value * 1664525 + 1013904223) >>> 0
    return value
  }
}

const randomText = next => Array.from({length: next() % 16}, () =>
  String.fromCodePoint([0x26, 0x3d, 0x23, 0x3f, 0x2b, 0x20, 0x4e2d, 0x1f600][next() % 8])
).join('')

test('fuzz: query values round-trip through URLSearchParams', () => {
  const next = random(0xa11123)
  for (let i = 0; i < 1000; i++) {
    const value = randomText(next)
    const result = withQuery('api/search', {query: value, index: i})
    const parsed = new URL(result, 'http://localhost/')
    assert.equal(parsed.searchParams.get('query'), value)
    assert.equal(parsed.searchParams.get('index'), String(i))
  }
})

test('absolute endpoint URLs preserve their origin and encode dynamic query values', () => {
  const endpoint = withQuery('https://ani.example/api/embyWebHook', {'api-key': 'token&a#b?'})
  const parsed = new URL(endpoint)

  assert.equal(parsed.origin, 'https://ani.example')
  assert.equal(parsed.pathname, '/api/embyWebHook')
  assert.equal(parsed.searchParams.get('api-key'), 'token&a#b?')
})

test('request keys distinguish read bodies and stay stable for equivalent JSON', () => {
  assert.equal(requestKey('api/mikan', 'POST', {a: 1}), requestKey('api/mikan', 'POST', {a: 1}))
  assert.notEqual(requestKey('api/mikan', 'POST', {a: 1}), requestKey('api/mikan', 'POST', {a: 2}))
})

test('dedupe only applies to safe reads unless explicitly overridden', () => {
  assert.equal(shouldDedupe('api/mikan?text=x', 'POST'), true)
  assert.equal(shouldDedupe('api/addAni', 'POST'), false)
  assert.equal(shouldDedupe('api/addAni', 'POST', {dedupe: true}), true)
  assert.equal(shouldDedupe('api/mikan', 'POST', {dedupe: false}), false)
})
