<template>
  <AddView ref="addRef"/>
  <el-dialog v-model="resourceVisible"
             align-center
             class="resource-dialog"
             width="760px">
    <template #header>
      <div class="resource-heading">
        <img v-if="resourceCover" :src="proxyImage(resourceCover)" :alt="resourceTitle" class="resource-cover"/>
        <div class="resource-heading-info">
          <div class="resource-title">{{ resourceTitle }}</div>
          <el-text size="small" type="info">{{ resourceGroups.length }} 个字幕组 · 点击右侧箭头查看可下载资源</el-text>
        </div>
      </div>
    </template>
    <div v-loading="resourceLoading" class="resource-dialog-content">
      <el-empty v-if="!resourceLoading && !resourceGroups.length" description="暂无可用 RSS"/>
      <el-collapse v-else v-model="expandedResourceGroups" class="rss-list">
        <el-collapse-item v-for="group in resourceGroups"
                          :key="groupKey(group)"
                          :name="groupKey(group)"
                          class="rss-card">
          <template #title>
            <div class="rss-card-header">
              <el-checkbox :model-value="isResourceSelected(group)"
                           class="resource-select"
                           @click.stop
                           @change="toggleResource(group)"/>
              <div class="rss-group-name">{{ groupName(group) }}</div>
              <el-text size="small" type="info" class="rss-date">{{ groupUpdate(group) }}</el-text>
              <div class="rss-tags">
                <el-tag v-for="tag in groupTags(group)" :key="tag" size="small">{{ tag }}</el-tag>
              </div>
              <el-tag size="small" type="info">
                {{ groupItems(group).length }}
              </el-tag>
              <el-button size="small" @click.stop="addSubscription(group)">
                <el-icon><Plus/></el-icon>
                添加
              </el-button>
            </div>
          </template>
          <div class="rss-card-body">
            <div class="rss-card-meta">
              <span v-if="groupStatus(group)">{{ groupStatus(group) }}</span>
              <span>RSS 订阅后会自动获取后续更新</span>
            </div>
            <div class="rss-url-row">
              <el-input :model-value="group.rss || ''" readonly size="small"/>
              <el-button size="small" @click="copyRss(group.rss)">复制 RSS</el-button>
              <el-button size="small" @click="openRss(group.rss)">打开</el-button>
            </div>
            <div class="download-list">
              <el-text v-if="!groupItems(group).length" size="small" type="info">暂无可下载资源</el-text>
              <div v-for="item in groupItems(group)" :key="resourceItemKey(item)" class="download-item">
                <div class="download-info">
                  <el-text class="download-title" truncated>{{ resourceItemTitle(item) }}</el-text>
                  <el-text size="small" type="info">{{ resourceItemMeta(item) }}</el-text>
                </div>
                <div class="download-actions">
                  <el-button v-if="item.magnet" size="small" text @click="copyRss(item.magnet)">复制磁力</el-button>
                  <el-button v-if="item.torrent" size="small" text @click="openRss(item.torrent)">种子</el-button>
                </div>
              </div>
            </div>
          </div>
        </el-collapse-item>
      </el-collapse>
    </div>
  </el-dialog>

  <div class="season-page app-page-layout">
    <PageHeaderView title="季度" :subtitle="`${sourceLabel} · ${seasonLabel || '选择季度'}`">
      <template #actions>
        <el-button :loading="loading" class="auto-button" icon="Refresh" @click="loadSource(true)">
          刷新
        </el-button>
      </template>
    </PageHeaderView>

    <div class="season-body app-page-content app-page-padding">
      <div class="season-toolbar">
        <el-radio-group v-model="source" size="default" @change="changeSource">
          <el-radio-button label="Mikan" value="mikan"/>
          <el-radio-button label="AniBT" value="ani-bt"/>
        </el-radio-group>
        <div class="season-toolbar-right">
          <el-select v-model="seasonValue"
                     class="season-select"
                     filterable
                     :loading="seasonLoading"
                     placeholder="选择季度"
                     @change="loadSource()">
            <el-option v-for="option in seasonOptions"
                       :key="option.value"
                       :label="option.label"
                       :value="option.value"/>
          </el-select>
          <el-tag type="info">{{ totalItems }} 部</el-tag>
          <el-text v-if="cacheUpdatedAt" class="cache-hint" size="small" type="info">
            缓存于 {{ cacheTimeLabel }} · 每日 03:00 更新
          </el-text>
        </div>
      </div>

      <div v-loading="loading" class="season-content">
        <el-empty v-if="!loading && !weeks.length" :description="loadError || '这个季度没有可用番剧'">
          <el-button v-if="loadError" type="primary" @click="loadSource()">重试</el-button>
        </el-empty>
        <el-tabs v-else v-model="activeWeek" class="week-tabs">
          <el-tab-pane v-for="week in weeks"
                       :key="week.weekLabel"
                       :label="week.weekLabel"
                       :name="week.weekLabel"
                       lazy>
            <el-scrollbar class="week-scrollbar">
              <div class="anime-grid">
                <el-card v-for="item in week.items"
                         :key="itemKey(item)"
                         class="anime-card"
                         shadow="never">
                  <div class="anime-card-content">
                    <img v-if="itemCover(item)"
                         :src="proxyImage(itemCover(item))"
                         :alt="itemTitle(item)"
                         class="anime-cover"
                         @click="openExternal(item)">
                    <div v-else class="anime-cover anime-cover-empty" @click="openExternal(item)">
                      <el-icon><Picture/></el-icon>
                    </div>
                    <div class="anime-info">
                      <el-tooltip :content="itemTitle(item)" placement="top">
                        <el-text class="anime-title" line-clamp="2" @click="openExternal(item)">
                          {{ itemTitle(item) }}
                        </el-text>
                      </el-tooltip>
                      <div class="anime-meta">
                        <el-tag v-if="itemScore(item)" type="warning" size="small">{{ itemScore(item) }}</el-tag>
                        <el-tag v-if="item.exists" type="success" size="small">已订阅</el-tag>
                      </div>
                      <el-button class="resource-button" bg text type="primary" @click="openResource(item)">
                        查看资源
                      </el-button>
                    </div>
                  </div>
                </el-card>
              </div>
            </el-scrollbar>
          </el-tab-pane>
        </el-tabs>
      </div>
    </div>
  </div>
</template>

<script setup>
import {computed, onBeforeUnmount, onMounted, ref} from "vue";
import {Picture, Plus} from "@element-plus/icons-vue";
import {ElMessage} from "element-plus";
import * as http from "@/js/http.js";
import {proxyImage} from "@/js/global.js";
import PageHeaderView from "@/view/custom/PageHeaderView.vue";
import AddView from "@/view/home/AddView.vue";
import {preserveSeasonOptions, readSeasonCache, writeSeasonCache} from "./seasonCatalogCache.js";

const source = ref('mikan')
const loading = ref(false)
const seasonLoading = ref(false)
const activeWeek = ref('')
const weeks = ref([])
const mikanSeasons = ref([])
const mikanSeason = ref('')
const aniBTSeasons = ref([])
const aniBTSeason = ref('')
const resourceVisible = ref(false)
const resourceLoading = ref(false)
const resourceTitle = ref('')
const resourceCover = ref('')
const resourceGroups = ref([])
const expandedResourceGroups = ref([])
const selectedResourceKeys = ref([])
const addRef = ref()
const cacheUpdatedAt = ref(0)
const loadError = ref('')

const SEASON_CACHE_TTL = 7 * 24 * 60 * 60 * 1000
let nightlyRefreshTimer
let loadSequence = 0

const sourceLabel = computed(() => source.value === 'mikan' ? 'Mikan' : 'AniBT')
const seasonOptions = computed(() => source.value === 'mikan'
    ? mikanSeasons.value.map(item => ({value: item.seasonLabel, label: item.seasonLabel}))
    : aniBTSeasons.value.map(item => ({value: item, label: item})))
const seasonValue = computed({
  get: () => source.value === 'mikan' ? mikanSeason.value : aniBTSeason.value,
  set: value => source.value === 'mikan' ? mikanSeason.value = value : aniBTSeason.value = value
})
const seasonLabel = computed(() => seasonValue.value)
const totalItems = computed(() => weeks.value.reduce((total, week) => total + (week.items || []).length, 0))
const cacheTimeLabel = computed(() => cacheUpdatedAt.value
    ? new Date(cacheUpdatedAt.value).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'})
    : '')

const readCache = (sourceName, season) => readSeasonCache(sourceName, season)
const writeCache = (sourceName, season, data) => {
  const savedAt = writeSeasonCache(sourceName, season, data)
  if (savedAt) cacheUpdatedAt.value = savedAt
}

const setWeeks = value => {
  weeks.value = value || []
  activeWeek.value = weeks.value[0]?.weekLabel || ''
}

const applyMikanData = data => {
  mikanSeasons.value = preserveSeasonOptions(mikanSeasons.value, data.seasons)
  if (!mikanSeason.value || !mikanSeasons.value.some(item => item.seasonLabel === mikanSeason.value)) {
    mikanSeason.value = mikanSeasons.value.find(item => item.select)?.seasonLabel
        || mikanSeasons.value[0]?.seasonLabel
        || ''
  }
  setWeeks(data.weeks)
}

const loadMikan = async (force = false, sequence = loadSequence) => {
  const requestedSeason = mikanSeason.value || 'current'
  const cached = readCache('mikan', requestedSeason)
  if (!force && cached && Date.now() - cached.savedAt < SEASON_CACHE_TTL) {
    if (sequence !== loadSequence) return
    applyMikanData(cached.data)
    cacheUpdatedAt.value = cached.savedAt
    seasonLoading.value = false
    loading.value = false
    return
  }
  seasonLoading.value = true
  loading.value = true
  loadError.value = ''
  try {
    const selected = mikanSeasons.value.find(item => item.seasonLabel === mikanSeason.value)
    const res = await http.mikan('', selected || {})
    if (sequence !== loadSequence) return
    const data = res.data || {}
    applyMikanData(data)
    writeCache('mikan', requestedSeason, data)
  } catch (e) {
    if (sequence !== loadSequence) return
    if (cached) {
      applyMikanData(cached.data)
      cacheUpdatedAt.value = cached.savedAt
      ElMessage.warning('网络请求失败，已显示缓存的季度数据')
    } else {
      loadError.value = '季度数据加载失败，请检查网络或代理设置后重试'
      ElMessage.error(e?.message || '加载季度数据失败')
    }
  } finally {
    if (sequence === loadSequence) {
      seasonLoading.value = false
      loading.value = false
    }
  }
}

const applyAniBTData = data => {
  aniBTSeasons.value = preserveSeasonOptions(aniBTSeasons.value, data.availableSeasons)
  aniBTSeason.value = data.requestedSeason || aniBTSeason.value || aniBTSeasons.value[0] || ''
  setWeeks((data.byWeekday || []).map(item => ({weekLabel: item.weekdayLabel, items: item.animes || []})))
}

const loadAniBT = async (force = false, sequence = loadSequence) => {
  const requestedSeason = aniBTSeason.value || 'current'
  const cached = readCache('ani-bt', requestedSeason)
  if (!force && cached && Date.now() - cached.savedAt < SEASON_CACHE_TTL) {
    if (sequence !== loadSequence) return
    applyAniBTData(cached.data)
    cacheUpdatedAt.value = cached.savedAt
    seasonLoading.value = false
    loading.value = false
    return
  }
  seasonLoading.value = true
  loading.value = true
  loadError.value = ''
  try {
    const res = await http.aniBT(aniBTSeason.value, '', '')
    if (sequence !== loadSequence) return
    const data = res.data || {}
    applyAniBTData(data)
    writeCache('ani-bt', requestedSeason, data)
  } catch (e) {
    if (sequence !== loadSequence) return
    if (cached) {
      applyAniBTData(cached.data)
      cacheUpdatedAt.value = cached.savedAt
      ElMessage.warning('网络请求失败，已显示缓存的季度数据')
    } else {
      loadError.value = '季度数据加载失败，请检查网络或代理设置后重试'
      ElMessage.error(e?.message || '加载季度数据失败')
    }
  } finally {
    if (sequence === loadSequence) {
      seasonLoading.value = false
      loading.value = false
    }
  }
}

const loadSource = (force = false) => {
  const sequence = ++loadSequence
  cacheUpdatedAt.value = 0
  loadError.value = ''
  return source.value === 'mikan' ? loadMikan(force, sequence) : loadAniBT(force, sequence)
}
const changeSource = () => {
  weeks.value = []
  activeWeek.value = ''
  loadSource()
}

const itemTitle = item => source.value === 'mikan'
    ? item.title
    : item.title?.primary || item.title?.chinese || item.title?.romaji || '未命名番剧'
const itemCover = item => item.cover || ''
const itemScore = item => {
  const score = Number(source.value === 'mikan' ? item.score : item.rating)
  return Number.isFinite(score) && score > 0 ? score.toFixed(1) : ''
}
const itemKey = item => source.value === 'mikan' ? item.url : item.bgmId || item.animeId

const openExternal = item => {
  const url = source.value === 'mikan' ? item.url : `https://anibt.net/anime/${item.bgmId || item.animeId}`
  if (url) window.open(url, '_blank', 'noopener')
}

const groupKey = group => group.groupId || group.subgroupId || group.slug || group.rss
const groupName = group => source.value === 'mikan' ? group.label : group.name
const groupItems = group => group.items || []
const groupUpdate = group => source.value === 'mikan' ? group.updateDay : (group.lastUpdatedAt ? new Date(group.lastUpdatedAt).toLocaleDateString() : '')
const groupStatus = group => source.value === 'ani-bt' ? group.status : ''

const openResource = async item => {
  resourceTitle.value = itemTitle(item)
  resourceCover.value = itemCover(item)
  resourceGroups.value = []
  expandedResourceGroups.value = []
  selectedResourceKeys.value = []
  resourceVisible.value = true
  resourceLoading.value = true
  try {
    const res = source.value === 'mikan'
        ? await http.mikanGroup(item.url)
        : await http.aniBTGroup(item.bgmId || item.animeId)
    resourceGroups.value = res.data || []
  } catch (e) {
    ElMessage.error(e?.message || '获取 RSS 资源失败')
  } finally {
    resourceLoading.value = false
  }
}

const resourceItemKey = item => item.releaseId || item.episodeKey || item.title
const resourceItemTitle = item => item.title || item.episodeKey || '未命名资源'
const resourceItemMeta = item => {
  const values = [item.formatSize, item.resolution, item.subtitle]
  const date = item.createdAt || item.publishedAt
  if (date) values.push(new Date(date).toLocaleString())
  return values.filter(Boolean).join(' · ')
}

const groupTags = group => (group.groupRegex?.tags || []).slice(0, 5)
const isResourceSelected = group => selectedResourceKeys.value.includes(groupKey(group))
const toggleResource = group => {
  const key = groupKey(group)
  selectedResourceKeys.value = isResourceSelected(group)
      ? selectedResourceKeys.value.filter(item => item !== key)
      : [...selectedResourceKeys.value, key]
}

const addSubscription = group => {
  if (!group.rss) {
    ElMessage.error('该字幕组没有可用 RSS')
    return
  }
  addRef.value?.showWithRss({
    type: source.value,
    url: group.rss,
    bgmUrl: group.bgmUrl || (group.bgmId ? `https://bgm.tv/subject/${group.bgmId}` : ''),
    subgroup: group.label || group.name || ''
  })
  resourceVisible.value = false
}

const copyRss = async rss => {
  if (!rss) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(rss)
    } else {
      const input = document.createElement('textarea')
      input.value = rss
      document.body.appendChild(input)
      input.select()
      document.execCommand('copy')
      input.remove()
    }
    ElMessage.success('RSS 地址已复制')
  } catch (e) {
    ElMessage.error('复制失败，请手动复制')
  }
}

const openRss = rss => {
  if (rss) window.open(rss, '_blank', 'noopener')
}

const scheduleNightlyRefresh = () => {
  clearTimeout(nightlyRefreshTimer)
  const now = new Date()
  const next = new Date(now)
  next.setHours(3, 0, 0, 0)
  if (next <= now) next.setDate(next.getDate() + 1)
  nightlyRefreshTimer = window.setTimeout(async () => {
    await loadSource(true)
    scheduleNightlyRefresh()
  }, next.getTime() - now.getTime())
}

onMounted(() => {
  loadSource()
  scheduleNightlyRefresh()
})
onBeforeUnmount(() => clearTimeout(nightlyRefreshTimer))
</script>

<style scoped>
.season-body { display: flex; flex-direction: column; gap: 10px; }
.season-toolbar { flex-shrink: 0; display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.season-toolbar-right { display: flex; align-items: center; gap: 8px; }
.season-select { width: 180px; }
.season-content { flex: 1; min-height: 0; overflow: hidden; }
.week-tabs, .week-scrollbar { height: 100%; }
.anime-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 10px; padding: 2px 2px 10px; }
.anime-card { min-width: 0; }
.anime-card-content { display: flex; gap: 12px; min-width: 0; }
.anime-cover { width: 82px; height: 116px; flex-shrink: 0; border-radius: 5px; object-fit: cover; cursor: pointer; background: var(--el-fill-color-light); }
.anime-cover-empty { display: flex; align-items: center; justify-content: center; color: var(--el-text-color-placeholder); }
.anime-info { min-width: 0; flex: 1; display: flex; flex-direction: column; align-items: flex-start; }
.anime-title { width: 100%; min-height: 42px; color: var(--el-text-color-primary); font-weight: 600; cursor: pointer; }
.anime-meta { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 10px; }
.resource-button { margin-top: auto; padding-left: 0; padding-right: 0; }
.resource-heading { display: flex; align-items: center; gap: 10px; }
.resource-cover { width: 42px; height: 58px; flex-shrink: 0; border-radius: 6px; object-fit: cover; }
.resource-heading-info { min-width: 0; }
.resource-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 16px; font-weight: 600; }
.resource-dialog-content { min-height: 120px; }
.rss-list { display: flex; flex-direction: column; gap: 6px; max-height: 60vh; overflow-y: auto; padding: 2px; }
.rss-card { border: 0; border-radius: 8px; background: #f5f7fa; }
.rss-card :deep(.el-collapse-item__header) { min-height: 54px; height: auto; padding: 7px 12px; border: 0; border-radius: 8px; background: #f5f7fa; }
.rss-card :deep(.el-collapse-item__wrap) { border-radius: 0 0 8px 8px; }
.rss-card :deep(.el-collapse-item__content) { padding: 0 12px 12px; background: #f5f7fa; }
.rss-card-header { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.resource-select { flex-shrink: 0; margin-right: 2px; }
.rss-group-name { min-width: 100px; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.rss-date { flex-shrink: 0; white-space: nowrap; }
.rss-tags { min-width: 0; display: flex; flex-wrap: nowrap; justify-content: flex-end; gap: 4px; margin-left: auto; overflow: hidden; }
.rss-tags .el-tag { color: var(--el-color-primary); border: 0; background: #eaf3ff; }
.rss-card-body { min-width: 0; }
.rss-card-meta { display: flex; gap: 10px; margin-top: 6px; color: var(--el-text-color-secondary); font-size: 12px; }
.rss-url-row { display: flex; align-items: center; gap: 6px; margin-top: 8px; }
.rss-url-row .el-input { min-width: 0; flex: 1; }
.cache-hint { white-space: nowrap; }
.download-list { display: flex; flex-direction: column; gap: 4px; margin-top: 10px; }
.download-item { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 7px 8px; border-radius: 6px; background: var(--el-fill-color-light); }
.download-info { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 3px; }
.download-title { width: 100%; }
.download-actions { display: flex; flex-shrink: 0; gap: 2px; }
@media (max-width: 800px) {
  .season-toolbar { align-items: stretch; flex-direction: column; }
  .season-toolbar-right { justify-content: space-between; }
  .season-select { flex: 1; }
  .anime-grid { grid-template-columns: 1fr; }
  .rss-url-row { flex-wrap: wrap; }
  .rss-url-row .el-input { flex-basis: 100%; }
  .rss-card-header { gap: 5px; }
  .rss-card-header .el-button { padding-left: 7px; padding-right: 7px; }
  .rss-date { display: none; }
  .rss-tags { display: none; }
  .download-item { align-items: flex-start; flex-direction: column; }
}
</style>
