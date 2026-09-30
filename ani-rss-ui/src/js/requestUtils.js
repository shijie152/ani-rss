export const readOnlyPostPaths = new Set([
  'api/config', 'api/listAni', 'api/about', 'api/mikan', 'api/mikanGroup',
  'api/aniBT', 'api/aniBTGroup', 'api/animeGardenList', 'api/animeGardenGroup',
  'api/logs', 'api/torrentsInfos', 'api/getThemoviedbName',
  'api/getThemoviedbGroup', 'api/getBgmTitle', 'api/searchBgm', 'api/playList',
  'api/getSubtitles', 'api/getAniBySubjectId', 'api/ping'
])

export const withQuery = (url, params = {}, base = 'http://localhost/') => {
  const parsed = new URL(url, base)
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null) parsed.searchParams.set(key, String(value))
  }
  return /^[a-z][a-z\d+.-]*:/i.test(url) ? parsed.toString() : `${parsed.pathname}${parsed.search}`
}

export const requestKey = (url, method, body) => {
  let serialized = ''
  if (body !== undefined && body !== null && body !== '') {
    try {
      serialized = JSON.stringify(body)
    } catch (_) {
      serialized = String(body)
    }
  }
  return `${method}:${url.replace(/^\/+/, '')}:${serialized}`
}

// withQuery 产出带前导斜杠的路径（/api/mikan），调用方也可能传裸路径（api/mikan）：
// 归一化只去掉前导斜杠，查询串必须保留（Mikan 搜索词的 text 就在 query 里，丢了会串号）
const endpointPath = url => url.split('?')[0].replace(/^\/+/, '')

export const shouldDedupe = (url, method, options = {}) => {
  if (options.dedupe !== undefined) return options.dedupe
  if (method === 'GET') return true
  return method === 'POST' && readOnlyPostPaths.has(endpointPath(url))
}
