// v3 invalidates v2 snapshots created before current-season aliases and
// subscription marker refreshes were made explicit.
const CACHE_VERSION = 'v3'
const CACHE_PREFIX = 'ani-rss:season-catalog:'
const MIKAN_SEARCH_CACHE_PREFIX = 'ani-rss:mikan-search:'

const storageOrDefault = storage => storage || globalThis.localStorage

export const resolveSeasonRequest = (season, followsCurrent) =>
  followsCurrent ? 'current' : (season || 'current')

export const seasonCacheKey = (source, season) =>
  `${CACHE_PREFIX}${CACHE_VERSION}:${source}:${encodeURIComponent(season || 'current')}`

export const mikanSearchCacheKey = text =>
  `${MIKAN_SEARCH_CACHE_PREFIX}${CACHE_VERSION}:${encodeURIComponent(String(text || '').trim().toLowerCase())}`

// Filtered catalogue responses may omit the season selector entirely. Keep
// the selector loaded by the initial catalogue request instead of replacing
// it with an empty array after the user changes season.
export const preserveSeasonOptions = (current, incoming) =>
  Array.isArray(incoming) && incoming.length > 0
    ? incoming
    : (Array.isArray(current) ? current : [])

// Filtered source responses omit the season selector. Keep the selector in
// the persisted snapshot so a reload can still choose another season.
export const withSeasonOptions = (source, data, options) => {
  if (!data || !Array.isArray(options) || options.length === 0) return data
  const field = source === 'mikan' ? 'seasons' : 'availableSeasons'
  if (Array.isArray(data[field]) && data[field].length > 0) return data
  return {...data, [field]: options}
}

const hasCatalogueItems = (source, data) => {
  const seasons = source === 'mikan' ? data?.seasons : data?.availableSeasons
  if (!Array.isArray(seasons)) return false

  if (source === 'mikan') {
    // Mikan's seasonal endpoint returns the catalogue without repeating the
    // dropdown options. The week cards are still a valid cache entry.
    if (seasons.length > 0 && !seasons.some(item => typeof item?.seasonLabel === 'string' && item.seasonLabel.length > 0)) return false
  } else if (!seasons.some(item => typeof item === 'string' && item.length > 0)) {
    return false
  }

  const weeks = [
    ...(Array.isArray(data?.weeks) ? data.weeks : []),
    ...(Array.isArray(data?.byWeekday) ? data.byWeekday : [])
  ]
  return weeks.some(week =>
    (source === 'mikan'
      && Array.isArray(week?.items)
      && week.items.some(item => typeof item?.title === 'string' && item.title.length > 0
        && typeof item?.url === 'string' && item.url.length > 0))
    || (source === 'ani-bt'
      && Array.isArray(week?.animes)
      && week.animes.some(item => item?.title && typeof item.title === 'object'
        && (item.bgmId || item.animeId)))
  )
}

export const readSeasonCache = (source, season, storage = undefined) => {
  try {
    const value = storageOrDefault(storage).getItem(seasonCacheKey(source, season))
    if (!value) return null
    const cached = JSON.parse(value)
    if (cached?.version !== CACHE_VERSION || !Number.isFinite(cached.savedAt) || !hasCatalogueItems(source, cached.data)) {
      return null
    }
    return cached
  } catch (e) {
    return null
  }
}

export const writeSeasonCache = (source, season, data, storage = undefined, now = Date.now()) => {
  if (!hasCatalogueItems(source, data)) return 0
  const cached = {version: CACHE_VERSION, savedAt: now, data}
  try {
    storageOrDefault(storage).setItem(seasonCacheKey(source, season), JSON.stringify(cached))
  } catch (e) {
    // 浏览器存储空间不足时不影响季度页面使用
  }
  return now
}

// The implicit "current" key is what a fresh page load reads before the
// source response has selected a concrete season. Keep it in sync when the
// user explicitly refreshes the current season, otherwise a reload can restore
// an older snapshot even though the concrete-season key was just updated.
export const writeSeasonCacheWithCurrentAlias = (
  source,
  season,
  data,
  storage = undefined,
  now = Date.now(),
  updateCurrentAlias = true
) => {
  const savedAt = writeSeasonCache(source, season, data, storage, now)
  if (savedAt && updateCurrentAlias && season !== 'current') {
    writeSeasonCache(source, 'current', data, storage, savedAt)
  }
  return savedAt
}

const isMikanSearchResponse = data => {
  if (!data || !Array.isArray(data.seasons) || !Array.isArray(data.weeks)) return false
  return data.totalItems === undefined || (Number.isFinite(data.totalItems) && data.totalItems >= 0)
}

export const readMikanSearchCache = (text, storage = undefined) => {
  try {
    const value = storageOrDefault(storage).getItem(mikanSearchCacheKey(text))
    if (!value) return null
    const cached = JSON.parse(value)
    if (cached?.version !== CACHE_VERSION || !Number.isFinite(cached.savedAt) || !isMikanSearchResponse(cached.data)) {
      return null
    }
    return cached
  } catch (e) {
    return null
  }
}

export const writeMikanSearchCache = (text, data, storage = undefined, now = Date.now()) => {
  if (!isMikanSearchResponse(data)) return 0
  const cached = {version: CACHE_VERSION, savedAt: now, data}
  try {
    storageOrDefault(storage).setItem(mikanSearchCacheKey(text), JSON.stringify(cached))
  } catch (e) {
    // 浏览器存储空间不足时不影响 Mikan 搜索页面使用
  }
  return now
}
