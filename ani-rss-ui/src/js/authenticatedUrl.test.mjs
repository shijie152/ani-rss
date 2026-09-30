import test from 'node:test'
import assert from 'node:assert/strict'
import {sessionUrl, apiKeyUrl} from './authenticatedUrl.js'

const base = 'http://ani-rss.test/'
const token = () => 'session-token'

test('session urls carry the token in the s parameter', () => {
  const url = new URL(sessionUrl('api/downloadLogs', {}, {base, token: token()}))
  assert.equal(url.pathname, '/api/downloadLogs')
  assert.equal(url.searchParams.get('s'), 'session-token')
})

test('session urls keep their own parameters', () => {
  const url = new URL(sessionUrl('api/file', {filename: '第01话.mkv'}, {base, token: token()}))
  assert.equal(url.searchParams.get('filename'), '第01话.mkv')
  assert.equal(url.searchParams.get('s'), 'session-token')
})

test('api key urls use the api-key parameter and never the session token', () => {
  const url = new URL(apiKeyUrl('api/embyWebHook', {key: 'abc123'}, {base}))
  assert.equal(url.pathname, '/api/embyWebHook')
  assert.equal(url.searchParams.get('api-key'), 'abc123')
  assert.equal(url.searchParams.get('s'), null)
})

test('credentials are url encoded', () => {
  const url = new URL(sessionUrl('api/downloadLogs', {}, {base, token: 'a&b=c'}))
  assert.equal(url.searchParams.get('s'), 'a&b=c')
})
