import {expect, test} from '@playwright/test'

const seasonResponse = {
  code: 200,
  t: 1_798_272_000_000,
  data: {
    seasons: [{seasonLabel: '2026 春', select: true}],
    weeks: [
      {
        weekLabel: '星期一',
        items: [
          {title: '视觉回归测试番剧', url: 'https://mikan.example/anime/1', cover: '', exists: false, score: 8.8},
          {title: '已订阅番剧', url: 'https://mikan.example/anime/2', cover: '', exists: true, score: 7.6}
        ]
      },
      {
        weekLabel: '星期五',
        items: [{title: '另一部番剧', url: 'https://mikan.example/anime/3', cover: '', exists: false}]
      }
    ]
  }
}

const resourceResponse = {
  code: 200,
  t: 1_798_272_000_000,
  data: [{
    label: '视觉字幕组',
    rss: 'https://mikan.example/rss/1',
    updateDay: '周一更新',
    groupRegex: {tags: ['简中', '1080p']},
    items: [{
      title: '[视觉字幕组] 视觉回归测试番剧 01',
      formatSize: '1.2 GiB',
      resolution: '1080p',
      subtitle: '简中',
      createdAt: '2026-09-25T08:00:00Z'
    }]
  }]
}

const freezeAnimations = async page => {
  await page.addStyleTag({content: `
    *, *::before, *::after {
      animation-duration: 0s !important;
      animation-delay: 0s !important;
      transition-duration: 0s !important;
      transition-delay: 0s !important;
      caret-color: transparent !important;
    }
    .cache-hint { visibility: hidden !important; }
  `})
}

const openSeasonPage = async (page, {dark = false} = {}) => {
  await page.addInitScript(() => {
    localStorage.clear()
    localStorage.setItem('authorization', 'visual-regression-token')
  })
  await page.route('**/api/mikan**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(seasonResponse)
  }))
  await page.route('**/api/mikanGroup**', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(resourceResponse)
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
  // 同 season-page.spec.mjs：首屏要等 SPA 与 Vite 预构建，默认 5 秒不够。
  await expect(page.getByRole('heading', {name: '季度'})).toBeVisible({timeout: 30_000})
  await expect(page.getByText('视觉回归测试番剧', {exact: true})).toBeVisible({timeout: 15_000})
  if (dark) await page.evaluate(() => document.documentElement.classList.add('dark'))
  await freezeAnimations(page)
}

const screenshotOptions = () => ({
  fullPage: true,
  animations: 'disabled',
  // Allow font rasterization noise without hiding layout changes: the season
  // selector and its count re-render a few hundred pixels differently across
  // font-smoothing states, while a layout change moves tens of thousands.
  maxDiffPixels: 400
})

test('季度页桌面浅色主题视觉回归', async ({page}) => {
  await page.setViewportSize({width: 1440, height: 900})
  await openSeasonPage(page)
  await expect(page).toHaveScreenshot('season-desktop-light.png', screenshotOptions())
})

test('季度页移动端深色主题视觉回归', async ({page}) => {
  await page.setViewportSize({width: 390, height: 844})
  await openSeasonPage(page, {dark: true})
  await expect(page).toHaveScreenshot('season-mobile-dark.png', screenshotOptions())
})

test('季度页资源弹窗视觉回归', async ({page}) => {
  await page.setViewportSize({width: 1440, height: 900})
  await openSeasonPage(page)
  await page.getByRole('button', {name: '查看资源'}).first().click()
  await expect(page.getByText('视觉字幕组', {exact: true})).toBeVisible()
  await page.getByText('视觉字幕组', {exact: true}).click()
  await expect(page.getByText('RSS 订阅后会自动获取后续更新')).toBeVisible()
  await expect(page).toHaveScreenshot('season-resource-dialog.png', screenshotOptions())
})
