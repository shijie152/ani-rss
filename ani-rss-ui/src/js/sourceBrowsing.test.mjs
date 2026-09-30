import test from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {copyText, createSubgroupLoader, sourceAdapters, submitBatch} from './sourceBrowsing.js'

// 直接测视图用的那一份 adapter，而不是在测试里另抄一份。
const mikanAdapter = sourceAdapters.mikan
const aniBTAdapter = sourceAdapters['ani-bt']

// 这些用例通过公开面 submitBatch 观察行为：分组、草稿形状与备用资源都不该
// 直接调内部函数（那会把测试绑死在实现上）。
const draftsFor = async (items, adapter) => {
  const drafts = []
  await submitBatch(items, adapter, {add: async draft => drafts.push(draft)})
  return drafts
}

test('one subscription per subject, with the extra sources as standby', async () => {
  const drafts = await draftsFor([
    {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1', label: 'A'},
    {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1', label: 'B'},
    {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=2', label: 'C'}
  ], mikanAdapter)

  assert.equal(drafts.length, 2)
  assert.equal(drafts[0].url, 'https://mikan.example/RSS/Bangumi?bangumiId=1')
  assert.equal(drafts[0].type, 'mikan')
  assert.deepEqual(drafts[0].match, [])
  assert.deepEqual(drafts[0].standbyRssList, [
    {label: 'B', url: 'https://mikan.example/RSS/Bangumi?bangumiId=1', offset: 0}
  ])
})

test('single source has no standby list', async () => {
  const drafts = await draftsFor(
    [{rss: 'https://mikan.example/RSS/Bangumi?bangumiId=3', label: 'C'}],
    mikanAdapter
  )
  assert.equal(drafts[0].standbyRssList, undefined)
})

test('copyText writes through the clipboard port', async () => {
  const written = []
  const clipboard = {writeText: async value => written.push(value)}

  const copied = await copyText('https://mikan.example/RSS/Bangumi?bangumiId=1', clipboard)

  assert.equal(copied, true)
  assert.deepEqual(written, ['https://mikan.example/RSS/Bangumi?bangumiId=1'])
})

test('copyText reports failure when neither clipboard nor fallback works', async () => {
  const failing = {writeText: async () => { throw new Error('denied') }}
  const copied = await copyText('rss-url', failing, {document: null})

  assert.equal(copied, false)
})

test('copyText falls back when the clipboard rejects', async () => {
  const failing = {writeText: async () => { throw new Error('denied') }}
  const document = {
    body: {appendChild() {}, removeChild() {}},
    createElement: () => ({value: '', select() {}}),
    execCommand: () => true
  }

  assert.equal(await copyText('rss-url', failing, {document}), true)
})

test('copyText falls back to a hidden input when the clipboard is unavailable', async () => {
  const appended = []
  const document = {
    body: {appendChild: node => appended.push(node), removeChild: node => appended.splice(appended.indexOf(node), 1)},
    createElement: () => ({value: '', select() { this.selected = true }}),
    execCommand: command => command === 'copy'
  }

  await copyText('rss-url', null, {document})

  assert.equal(appended.length, 0)
  assert.equal(document.execCommand('copy'), true)
})

// 三个源站都必须有 adapter，且视图不得自己再定义一份（那会与这份漂移）。
// 视图必须引用 module 里的 adapter：自己再写一份 definition 会与这份漂移，
// 而视图文件在 Node 里加载不了，测试只能测到 module 这一份。
test('views reference the shared adapters instead of defining their own', async () => {
  const dir = fileURLToPath(new URL('../view/home/', import.meta.url))
  for (const name of ['MikanView.vue', 'AniBTView.vue', 'AnimeGardenView.vue']) {
    const source = await readFile(join(dir, name), 'utf8')
    assert.ok(source.includes('sourceAdapters'), name + ' must use the shared adapters')
    assert.ok(!/^const \w*[Aa]dapter = \{/m.test(source), name + ' must not define its own adapter')
  }
})

test('every source has an adapter defined in the module', () => {
  assert.deepEqual(Object.keys(sourceAdapters).sort(), ['ani-bt', 'anime-garden', 'mikan'])
  for (const [name, adapter] of Object.entries(sourceAdapters)) {
    assert.equal(adapter.type, name, name + '.type')
    assert.equal(typeof adapter.subjectId, 'function', name + '.subjectId')
    assert.equal(typeof adapter.subgroupLabel, 'function', name + '.subgroupLabel')
  }
})

// Mikan 的批量添加草稿刻意不带 bgmUrl/subgroup：后端只有两者都为空时才去
// 解析 Mikan 详情页；带上会静默跳过那段解析（见 source/mikan.go 的注释）。
// 字幕组加载器：三个源站页共用，重复展开同一个字幕组不能再发请求。
test('subgroup loader caches results and reports loading state', async () => {
  const states = []
  let calls = 0
  const loader = createSubgroupLoader({
    load: async url => { calls++; return {url} },
    onLoading: value => states.push(value)
  })

  const first = await loader.load('https://mikan.example/g1')
  const second = await loader.load('https://mikan.example/g1')

  assert.equal(calls, 1)
  assert.deepEqual(first, second)
  assert.equal(loader.has('https://mikan.example/g1'), true)
  assert.deepEqual(states, [true, false])
})

test('subgroup loader coalesces concurrent loads of the same key', async () => {
  let calls = 0
  let release
  const loader = createSubgroupLoader({
    load: () => { calls++; return new Promise(resolve => { release = resolve }) }
  })
  const a = loader.load('u')
  const b = loader.load('u')
  // load 在微任务里才被调用，先让出一次事件循环让 release 就位。
  await Promise.resolve()
  release({ok: true})
  assert.deepEqual(await a, {ok: true})
  assert.deepEqual(await b, {ok: true})
  assert.equal(calls, 1)
})

test('mikan drafts omit bgmUrl and subgroup so the backend resolves the detail page', async () => {
  const drafts = await draftsFor([
    {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1', label: '字幕组A'}
  ], mikanAdapter)

  assert.equal(drafts[0].bgmUrl, undefined)
  assert.equal(drafts[0].subgroup, undefined)
  assert.equal(drafts[0].type, 'mikan')
})

test('ani-bt and anime-garden drafts do carry bgmUrl and subgroup', async () => {
  for (const adapter of [aniBTAdapter, sourceAdapters['anime-garden']]) {
    const drafts = await draftsFor(
      [{rss: 'https://source.example/rss?bgmId=7', bgmId: 7, name: '字幕组X'}],
      adapter
    )
    assert.equal(drafts[0].bgmUrl, 'https://bgm.tv/subject/7', adapter.type)
    assert.equal(drafts[0].subgroup, '字幕组X', adapter.type)
  }
})

test('anime-garden adapter reads the subject from the item itself', async () => {
  const drafts = await draftsFor(
    [{rss: 'https://animegarden.example/rss', bgmId: 42, name: '字幕组G'}],
    sourceAdapters['anime-garden']
  )
  assert.equal(drafts[0].type, 'anime-garden')
  assert.equal(drafts[0].subgroup, '字幕组G')
  assert.equal(drafts[0].bgmUrl, 'https://bgm.tv/subject/42')
})

test('ani-bt adapter carries its own type, subgroup field and bgm url', async () => {
  const drafts = await draftsFor(
    [{rss: 'https://anibt.example/rss?bgmId=42', name: '字幕组A'}],
    aniBTAdapter
  )

  assert.equal(drafts[0].type, 'ani-bt')
  assert.equal(drafts[0].subgroup, '字幕组A')
  assert.equal(drafts[0].bgmUrl, 'https://bgm.tv/subject/42')
})

test('submits one subscription per subject and reports progress', () => {
  const submitted = []
  const progress = []
  return submitBatch(
    [
      {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1', label: 'A'},
      {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1', label: 'B'},
      {rss: 'https://mikan.example/RSS/Bangumi?bangumiId=2', label: 'C'}
    ],
    mikanAdapter,
    {
      resolve: async subscription => ({...subscription, title: '解析后的番剧'}),
      add: async subscription => submitted.push(subscription),
      onProgress: done => progress.push(done)
    }
  ).then(() => {
    assert.equal(submitted.length, 2)
    assert.equal(submitted[0].title, '解析后的番剧')
    assert.deepEqual(progress, [2, 3])
  })
})
