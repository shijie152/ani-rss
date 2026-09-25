import test from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'

const read = file => readFile(new URL(file, import.meta.url), 'utf8')

test('lazy loading contracts remain enabled for the performance paths', async () => {
  const router = await read('./router/index.js')
  const entry = await read('./main.js')
  const image = await read('./view/custom/LazyImage.vue')
  const season = await read('./view/home/SeasonCatalogView.vue')
  const subscription = await read('./view/home/SubscriptionListView.vue')
  const logs = await read('./view/home/LogsView.vue')

  assert.match(router, /import\('@\/view\/home\/DashboardView\.vue'\)/)
  assert.doesNotMatch(router, /import DashboardView from/)
  assert.doesNotMatch(entry, /import \* as ElementPlusIconsVue/)
  assert.match(image, /loading="lazy"/)
  assert.match(image, /decoding="async"/)
  assert.match(season, /<LazyImage/)
  assert.match(subscription, /content-visibility: auto/)
  assert.match(logs, /content-visibility: auto/)
})
