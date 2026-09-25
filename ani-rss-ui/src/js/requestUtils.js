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
  return `${method}:${url}:${serialized}`
}

export const shouldDedupe = (url, method, options = {}) => {
  if (options.dedupe !== undefined) return options.dedupe
  if (method === 'GET') return true
  return method === 'POST' && readOnlyPostPaths.has(url.split('?')[0])
}
