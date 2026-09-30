import assert from 'node:assert/strict'
import {expect, test} from '@playwright/test'

// 三个源站页共用同一份浏览 module（分组 / 批量添加 / 复制）。这条冒烟在真实
// 浏览器里各走一遍「打开 → 渲染」，路径或 adapter 接错都会在这里失败。
const sources = [
  {tab: 'Mikan', label: 'Mikan'},
  {tab: 'AniBT', label: 'AniBT'},
  {tab: 'AnimeGarden', label: 'AnimeGarden'}
]

// 每个源站的列表响应与字幕组条目形状；批量添加走同一 submitBatch，但 adapter 不同，
// 所以三个源站都要真跑一遍，而不是只看对话框能打开。
const batchCases = [
  {
    tab: 'Mikan',
    label: 'Mikan',
    listRoute: '**/api/mikan**',
    listBody: {
      seasons: [{seasonLabel: '2026 春', select: true}],
      weeks: [{weekLabel: '星期一', items: [{title: '冒烟番剧', url: 'https://mikan.example/a/1', cover: '', exists: false}]}]
    },
    groupRoute: '**/api/mikanGroup**',
    groupBody: [{label: '字幕组A', updateDay: '周一', rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1', bgmUrl: 'https://bgm.tv/subject/1', groupRegex: {regexList: [[]], tags: []}}]
  },
  {
    tab: 'AniBT',
    label: 'AniBT',
    listRoute: '**/api/aniBT**',
    listBody: {
      requestedSeason: '2026 春',
      availableSeasons: ['2026 春'],
      byWeekday: [{weekdayLabel: '星期一', animes: [{title: {primary: '冒烟番剧'}, bgmId: 2, rss: 'https://anibt.example/rss?bgmId=2'}]}]
    },
    groupRoute: '**/api/aniBTGroup**',
    groupBody: [{name: '字幕组B', updateDay: '周一', rss: 'https://anibt.example/rss?bgmId=2', bgmId: 2, groupRegex: {regexList: [[]], tags: []}}]
  },
  {
    tab: 'AnimeGarden',
    label: 'AnimeGarden',
    listRoute: '**/api/animeGardenList**',
    listBody: [],
    groupRoute: '**/api/animeGardenGroup**',
    groupBody: [{name: '字幕组G', updateDay: '周一', rss: 'https://animegarden.example/rss', bgmId: 3, groupRegex: {regexList: [[]], tags: []}}]
  }
]

const mikanResponse = {
  code: 200,
  t: Date.now(),
  data: {
    seasons: [{seasonLabel: '2026 春', select: true}],
    weeks: [{
      weekLabel: '星期一',
      items: [{title: '源站冒烟番剧', url: 'https://mikan.example/anime/1', cover: '', exists: false}]
    }]
  }
}

// 批量添加是三个源站共用的 submitBatch 路径：勾选字幕组条目 → 点「批量添加」，
// 必须发出 rssToAni + addAni，而不是只把对话框打开。
test('Mikan 批量添加走 submitBatch（rssToAni + addAni）', async ({page}) => {
  const group = {
    label: '字幕组A',
    updateDay: '星期一',
    rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1',
    bgmUrl: 'https://bgm.tv/subject/1',
    groupRegex: {regexList: [[{label: '1080p', regex: '1080p'}]], tags: ['1080p']},
    items: [{title: '第01话 1080p', magnet: 'magnet:?xt=urn:btih:smoke', torrent: 'https://mikan.example/1.torrent', formatSize: '1.2 GiB', createdAt: '2026-09-30'}]
  }
  const called = {rssToAni: 0, addAni: 0}
  await page.addInitScript(() => localStorage.setItem('authorization', 'browser-smoke-token'))
  await page.route('**/api/mikan**', route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify(mikanResponse)
  }))
  await page.route('**/api/mikanGroup**', route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: [group]})
  }))
  await page.route('**/api/rssToAni', route => {
    called.rssToAni++
    return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: {id: '1', title: '番剧'}})})
  })
  await page.route('**/api/addAni', route => {
    called.addAni++
    return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: null})})
  })
  await page.route('**/api/custom.css', route => route.fulfill({status: 200, contentType: 'text/css', body: ''}))
  await page.route('**/api/custom.js', route => route.fulfill({status: 200, contentType: 'application/javascript', body: ''}))
  await page.route('**/api/listAni', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: []})}))
  await page.route('**/api/config', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: {}})}))
  // 批量添加成功后页面会 reload；用路由拦不住，改断言请求发生即可。
  await page.route('**/api/login', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: 'token'})}))

  await page.goto('/#/subscriptions')
  await page.getByRole('button', {name: '添加'}).click()
  await page.getByRole('menuitem', {name: '添加订阅'}).click()
  await page.getByRole('button', {name: '浏览 Mikan'}).click()
  await expect(page.getByRole('dialog', {name: 'Mikan'})).toBeVisible()

  // 展开番剧卡片，加载字幕组，勾选后批量添加。
  await page.getByText('源站冒烟番剧').first().click()
  const checkbox = page.locator('.group-checkbox-wrapper input[type=checkbox]').first()
  await checkbox.waitFor({state: 'attached', timeout: 10_000})
  // 复选框在折叠面板里，可能不在视口内；直接派发点击事件驱动 v-model。
  await checkbox.evaluate(node => node.click())
  await page.getByRole('button', {name: '批量添加'}).click()

  await expect.poll(() => called.rssToAni, {timeout: 10_000}).toBeGreaterThan(0)
  await expect.poll(() => called.addAni, {timeout: 10_000}).toBeGreaterThan(0)
})

// 批量添加走同一份 submitBatch，但三个源站的 adapter 不同（分组键、字幕组字段、
// 是否补 bgmUrl/subgroup 都不同），所以 Mikan 与 AniBT 各真跑一遍。
for (const source of batchCases.filter(item => item.label !== 'AnimeGarden')) {
  test(`${source.label} 批量添加走 submitBatch`, async ({page}) => {
    const called = {rssToAni: 0, addAni: 0}
    const drafts = []
    const added = []
    await page.addInitScript(() => localStorage.setItem('authorization', 'browser-smoke-token'))
    await page.route(source.listRoute, route => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({code: 200, t: Date.now(), data: source.listBody})
    }))
    await page.route(source.groupRoute, route => route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({code: 200, t: Date.now(), data: source.groupBody})
    }))
    // rssToAni 收到的是草稿：源站差异（Mikan 不带 bgmUrl/subgroup）在这里可断言。
    await page.route('**/api/rssToAni', route => {
      called.rssToAni++
      drafts.push(route.request().postDataJSON())
      return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: {id: '1', title: '番剧'}})})
    })
    await page.route('**/api/addAni', async route => {
      called.addAni++
      added.push(route.request().postDataJSON())
      return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: null})})
    })
    await page.route('**/api/custom.css', route => route.fulfill({status: 200, contentType: 'text/css', body: ''}))
    await page.route('**/api/custom.js', route => route.fulfill({status: 200, contentType: 'application/javascript', body: ''}))
    await page.route('**/api/listAni', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: []})}))
    await page.route('**/api/config', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: {}})}))

    await page.goto('/#/subscriptions')
    await page.getByRole('button', {name: '添加'}).click()
    await page.getByRole('menuitem', {name: '添加订阅'}).click()
    await page.getByRole('tab', {name: source.tab}).click()
    await page.getByRole('button', {name: `浏览 ${source.label}`}).click()
    await expect(page.getByRole('dialog', {name: source.label})).toBeVisible()

    await page.getByText('冒烟番剧').first().click()
    const checkbox = page.locator('.group-checkbox-wrapper input[type=checkbox]').first()
    await checkbox.waitFor({state: 'attached', timeout: 10_000})
    await checkbox.evaluate(node => node.click())
    await page.getByRole('button', {name: '批量添加'}).click()

    await expect.poll(() => called.rssToAni, {timeout: 10_000}).toBeGreaterThan(0)
    await expect.poll(() => called.addAni, {timeout: 10_000}).toBeGreaterThan(0)
    // Mikan 的草稿刻意不带 bgmUrl/subgroup（后端据此走详情页解析）；其他源站要带。
    if (source.label === 'Mikan') {
      assert.equal(drafts[0].bgmUrl, undefined)
      assert.equal(drafts[0].subgroup, undefined)
    } else {
      assert.ok(drafts[0].bgmUrl, `${source.label} 草稿应带 bgmUrl`)
    }
  })
}

// 验收四步的最后一步：复制 RSS/磁力链。走 copyText（Clipboard 优先，失败回退）。
test('Mikan 资源条目可以复制磁力链', async ({page}) => {
  const group = {
    label: '字幕组A',
    updateDay: '星期一',
    rss: 'https://mikan.example/RSS/Bangumi?bangumiId=1',
    bgmUrl: 'https://bgm.tv/subject/1',
    groupRegex: {regexList: [[{label: '1080p', regex: '1080p'}]], tags: ['1080p']},
    items: [{title: '第01话 1080p', magnet: 'magnet:?xt=urn:btih:smoke', torrent: 'https://mikan.example/1.torrent', formatSize: '1.2 GiB', createdAt: '2026-09-30'}]
  }
  await page.addInitScript(() => {
    localStorage.setItem('authorization', 'browser-smoke-token')
    // 记录剪贴板写入：断言复制真的把磁力链交出去了。
    window.__copied = []
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {writeText: async value => { window.__copied.push(value) }}
    })
  })
  await page.route('**/api/mikan**', route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify(mikanResponse)
  }))
  await page.route('**/api/mikanGroup**', route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: [group]})
  }))
  await page.route('**/api/custom.css', route => route.fulfill({status: 200, contentType: 'text/css', body: ''}))
  await page.route('**/api/custom.js', route => route.fulfill({status: 200, contentType: 'application/javascript', body: ''}))
  await page.route('**/api/listAni', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: []})}))
  await page.route('**/api/config', route => route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({code: 200, t: Date.now(), data: {}})}))

  await page.goto('/#/subscriptions')
  await page.getByRole('button', {name: '添加'}).click()
  await page.getByRole('menuitem', {name: '添加订阅'}).click()
  await page.getByRole('button', {name: '浏览 Mikan'}).click()
  await expect(page.getByRole('dialog', {name: 'Mikan'})).toBeVisible()

  await page.getByText('源站冒烟番剧').first().click()
  const copyButton = page.locator('.item-footer button').first()
  await copyButton.waitFor({state: 'attached', timeout: 10_000})
  await copyButton.evaluate(node => node.click())

  await expect.poll(() => page.evaluate(() => window.__copied), {timeout: 10_000})
      .toEqual(['magnet:?xt=urn:btih:smoke'])
})

test('每个源站页都能打开并渲染', async ({page}) => {
  await page.addInitScript(() => localStorage.setItem('authorization', 'browser-smoke-token'))
  await page.route('**/api/mikan**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(mikanResponse)
  }))
  await page.route('**/api/aniBT**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      code: 200,
      t: Date.now(),
      data: {
        requestedSeason: '2026 春',
        availableSeasons: ['2026 春'],
        byWeekday: [{weekdayLabel: '星期一', animes: [{title: {中文: '源站冒烟番剧'}, bgmId: 1, rss: 'https://anibt.example/rss?bgmId=1'}]}]
      }
    })
  }))
  await page.route('**/api/animeGarden**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({code: 200, t: Date.now(), data: []})
  }))
  await page.route('**/api/custom.css', route => route.fulfill({status: 200, contentType: 'text/css', body: ''}))
  await page.route('**/api/custom.js', route => route.fulfill({status: 200, contentType: 'application/javascript', body: ''}))

  await page.route('**/api/listAni', route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({code: 200, t: Date.now(), data: []})
  }))
  await page.route('**/api/config', route => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({code: 200, t: Date.now(), data: {}})
  }))

  await page.goto('/#/subscriptions')
  await page.getByRole('button', {name: '添加'}).click()
  await page.getByRole('menuitem', {name: '添加订阅'}).click()

  for (const source of sources) {
    await page.getByRole('tab', {name: source.tab}).click()
    // 三个源站页共用同一份浏览 module；adapter 接错或路径写错都打不开。
    await page.getByRole('button', {name: `浏览 ${source.label}`}).click()
    // 三个源站视图同时挂载，各自打开自己的对话框：标题是每个源站最稳定的锚点
    // （AnimeGarden 没有搜索框，用搜索框会误判）。
    await expect(page.getByRole('dialog', {name: source.label})).toBeVisible({timeout: 10_000})
    await page.keyboard.press('Escape')
  }
})
