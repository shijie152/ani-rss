import {expect, test} from '@playwright/test'

// 三个源站页共用同一份浏览 module（分组 / 批量添加 / 复制）。这条冒烟在真实
// 浏览器里各走一遍「打开 → 渲染」，路径或 adapter 接错都会在这里失败。
const sources = [
  {tab: 'Mikan', label: 'Mikan'},
  {tab: 'AniBT', label: 'AniBT'},
  {tab: 'AnimeGarden', label: 'AnimeGarden'}
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
