const CACHE_VERSION = 'v2'
const CACHE_PREFIX = 'ani-rss:season-catalog:'

const storageOrDefault = storage => storage || globalThis.localStorage

export const seasonCacheKey = (source, season) =>
  `${CACHE_PREFIX}${CACHE_VERSION}:${source}:${encodeURIComponent(season || 'current')}`

// Filtered catalogue responses may omit the season selector entirely. Keep
// the selector loaded by the initial catalogue request instead of replacing
// it with an empty array after the user changes season.
export const preserveSeasonOptions = (current, incoming) =>
  Array.isArray(incoming) && incoming.length > 0
    ? incoming
    : (Array.isArray(current) ? current : [])

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
