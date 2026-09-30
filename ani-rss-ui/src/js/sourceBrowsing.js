// 源站浏览 module：搜索→字幕组分组→匹配弹窗→批量添加→复制 RSS 这套交互
// 只实现一遍。每个源站是一个薄 adapter：只要给出分组键、字幕组字段名和
// 订阅类型，就能接入同一份实现。

// 三个源站的 adapter 定义在这里，而不是散在各视图里：视图 import 它们，
// 测试也直接测这一份，避免"测试复制一份、真实那份没人测"的漂移。
export const sourceAdapters = {
  mikan: {
    type: 'mikan',
    subjectId: item => new URL(item.rss).searchParams.get('bangumiId'),
    subgroupLabel: item => item.label
  },
  'ani-bt': {
    type: 'ani-bt',
    subjectId: item => new URL(item.rss).searchParams.get('bgmId'),
    subgroupLabel: item => item.name,
    bgmUrl: subject => `https://bgm.tv/subject/${subject}`
  },
  'anime-garden': {
    type: 'anime-garden',
    subjectId: item => item.bgmId,
    subgroupLabel: item => item.name,
    bgmUrl: subject => `https://bgm.tv/subject/${subject}`
  }
}

// 选中资源按番剧（bgm 条目）归组：同一部番剧的字幕组条目会合并成一条订阅，
// 除主资源外的条目成为备用资源。
const groupBySubject = (items, adapter) =>
  items.reduce((grouped, item) => {
    const subject = adapter.subjectId(item)
    if (subject === null || subject === undefined || subject === '') return grouped
    grouped[subject] = grouped[subject] || []
    grouped[subject].push(item)
    return grouped
  }, {})

const buildSubscription = (items, adapter) => {
  const [primary, ...standby] = items
  const subscription = {
    url: primary.rss,
    season: 1,
    offset: 0,
    title: '',
    exclude: [],
    totalEpisodeNumber: 0,
    match: [],
    type: adapter.type
  }
  const bgmUrl = adapter.bgmUrl
      ? adapter.bgmUrl(adapter.subjectId(primary))
      : primary.bgmUrl
  if (bgmUrl) subscription.bgmUrl = bgmUrl
  const subgroup = adapter.subgroupLabel(primary)
  if (subgroup) subscription.subgroup = subgroup
  if (standby.length > 0) {
    subscription.standbyRssList = standby.map(item => ({
      label: adapter.subgroupLabel(item),
      url: item.rss,
      offset: 0
    }))
  }
  return subscription
}

// resolve 把订阅草稿补成完整番剧（对应 rssToAni），add 落库（对应 addAni），
// onProgress 汇报已完成条数，供批量添加弹窗显示进度。
export const submitBatch = async (items, adapter, {resolve, add, onProgress} = {}) => {
  const grouped = groupBySubject(items, adapter)
  let done = 0
  for (const group of Object.values(grouped)) {
    const draft = buildSubscription(group, adapter)
    const resolved = resolve ? await resolve(draft) : draft
    if (add) await add(resolved)
    done += group.length
    if (onProgress) onProgress(done)
  }
  return done
}

// 复制 RSS 地址：优先用 Clipboard API，不可用（非安全上下文、旧浏览器）
// 时回退到临时 input + execCommand。两个依赖都可注入，便于测试。
// 返回是否复制成功：调用方据此提示，无需自己再写一份降级逻辑。
export const copyText = async (value, clipboard = globalThis.navigator?.clipboard, {document = globalThis.document} = {}) => {
  if (clipboard?.writeText) {
    try {
      await clipboard.writeText(value)
      return true
    } catch (e) {
      // 落到下面的降级路径
    }
  }
  if (!document?.execCommand) return false
  const input = document.createElement('input')
  input.value = value
  document.body.appendChild(input)
  input.select()
  const copied = document.execCommand('copy')
  document.body.removeChild(input)
  return copied
}
