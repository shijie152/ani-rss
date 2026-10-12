import {expect, test} from '@playwright/test'

const seasonResponse = {
  code: 200,
  t: Date.now(),
  data: {
    seasons: [{seasonLabel: '2026 春', select: true}],
    weeks: [{
      weekLabel: '星期一',
      items: [{title: 'Smoke Test 番剧', url: 'https://mikan.example/anime/1', cover: '', exists: false}]
    }]
  }
}

test('季度页通过动态路由加载并渲染 mock Mikan 数据', async ({page}) => {
  await page.addInitScript(() => localStorage.setItem('authorization', 'browser-smoke-token'))
  await page.route('**/api/mikan**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(seasonResponse)
  }))
  await page.route('**/api/custom.css', route => route.fulfill({
    status: 200,
    contentType: 'text/css',
    body: ''
  }))
  await page.route('**/api/custom.js', route => route.fulfill({
    status: 200,
    contentType: 'application/javascript',
    body: ''
  }))

  await page.goto('/#/seasons')
  // 首屏要等整个 SPA 起来；CI 上首次访问还要等 Vite 预构建依赖，默认 5 秒不够
  // （实测某次 CI 就卡在第一条断言上，同一轮其它用例因为服务已热都通过）。
  await expect(page.getByRole('heading', {name: '季度'})).toBeVisible({timeout: 30_000})
  await expect(page.getByText('Mikan · 2026 春')).toBeVisible({timeout: 15_000})
  await expect(page.getByText('Smoke Test 番剧', {exact: true})).toBeVisible()
  await expect(page.getByText('星期一', {exact: true})).toBeVisible()
})
