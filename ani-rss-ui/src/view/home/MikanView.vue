<template>
  <el-dialog v-model="batchAdditionDialogVisible" align-center center title="正在批量添加订阅"
             width="500"
             :close-on-click-modal="false"
             :close-on-press-escape="false"
             :show-close="false">
    <div>
      <el-progress :percentage="Number.parseInt((batchAdditionNum / rssList.length) * 100.0)"/>
    </div>
    <div>
      {{ batchAdditionNum }} / {{ rssList.length }}
    </div>
  </el-dialog>
  <el-dialog v-model="matchDialogVisible" align-center center title="匹配" width="auto">
    <div class="match-content">
      <el-radio-group v-model="addAni.match">
        <div v-for="regexItems in regexList" class="match-item">
          <el-radio :label="JSON.stringify(regexItems)"
                    :value="JSON.stringify(regexItems.map(it => it.regex))">
            <el-tag v-if="regexItems.length" v-for="regexItem in regexItems" class="tag-margin">
              {{ regexItem.label }}
            </el-tag>
            <el-tag v-else type="success">全部</el-tag>
          </el-radio>
        </div>
      </el-radio-group>
    </div>
    <div class="dialog-footer">
      <el-button icon="Check" @click="async ()=>{
          emit('callback', addAni)
          dialogVisible = false
          matchDialogVisible = false
      }" text bg>确定
      </el-button>
    </div>
  </el-dialog>
  <el-dialog v-model="dialogVisible" center title="Mikan">
    <el-checkbox-group v-model="rssList">
      <div class="content-wrapper">
        <div class="search-section">
          <div class="search-header">
            <el-input v-model:model-value="text" clearable placeholder="请输入搜索标题"
                      prefix-icon="Search"
                      @clear="()=>{
                        text = ''
                        search()
                      }"
                      @keyup.enter="search"/>
            <el-button :loading="searchLoading" bg icon="Search" text @click="search">搜索</el-button>
          </div>
          <div v-if="data.seasons.length" class="flex season-selector">
            <el-select v-model:model-value="seasonSelect" class="season-select"
                       :disabled="text.length > 0 || loading"
                       @change="change">
              <el-option v-for="season in data.seasons" :key="season['seasonLabel']"
                         :label="season['seasonLabel']" :value="season['seasonLabel']"/>
            </el-select>
            <el-button :disabled="rssList.length < 1" bg icon="Plus" text @click="batchAddition">批量添加</el-button>
          </div>
        </div>
        <div v-loading="loading" class="scroll-container">
          <el-tabs v-model="activeName" class="week-tabs">
            <el-tab-pane v-for="week in data.weeks" :key="week.weekLabel"
                         :label="week.weekLabel" :name="week.weekLabel" lazy>
              <el-scrollbar class="week-pane-scrollbar">
                <div class="collapse-content">
                  <el-collapse accordion @change="collapseChange">
                    <el-collapse-item v-for="it in week.items" :name="it.url">
                      <template #title>
                        <div class="flex collapse-title">
                          <LazyImage :src="proxyImage(it['cover'])" :alt="it.title" class-name="cover" @click.stop="open(it.url)"/>
                          <div class="flex collapse-title">
                            <el-text :truncated="false" line-clamp="1" size="small"
                                     class="title-text">
                              {{ it.title }}
                            </el-text>
                          </div>
                          <div v-if="it['score'] > 0" class="score-margin">
                            <h4 class="score-color">
                              {{ it['score'].toFixed(1) }}
                            </h4>
                          </div>
                          <el-badge v-if="it['exists']" class="item badge-margin" type="primary"
                                    value="已订阅"/>
                        </div>
                      </template>
                      <div v-if="selectName === it.url" v-loading="groupLoading"
                           class="group-content">
                        <el-collapse accordion>
                          <el-collapse-item v-for="group in groups[it.url]">
                            <template #title>
                              <div class="group-title-wrapper">
                                <div class="group-checkbox-wrapper">
                                  <el-checkbox :value="JSON.stringify(group)" class="checkbox-margin" @click.stop/>
                                </div>
                                <div class="group-label">
                                  <el-text style="max-width: 100px;" truncated>{{ group.label }}</el-text>
                                  &nbsp;
                                  <el-text class="mx-1" size="small">{{ group['updateDay'] }}</el-text>
                                </div>
                                <div v-if="showTag()">
                                  <el-tag v-for="tag in group['groupRegex']['tags']"
                                          class="tag-margin">
                                    {{ tag }}
                                  </el-tag>
                                </div>
                                <div class="group-action">
                                  <el-button bg @click.stop="callback(group)" icon="Plus">
                                    添加
                                  </el-button>
                                </div>
                              </div>
                            </template>
                            <div class="group-items">
                              <div v-for="ti in group.items" class="item-margin">
                                <el-card shadow="never">
                                  <div>
                                    <h5>
                                      {{ ti.title }}
                                    </h5>
                                    <div class="item-footer">
                                      <p>
                                        {{ ti['formatSize'] }}
                                        {{ ti['createdAt'] }}
                                      </p>
                                      <div>
                                        <el-button :icon="DocumentCopy" bg text @click="copy(ti['magnet'])"/>
                                        <el-button :icon="DownloadIcon" bg text @click="openUrl(ti['torrent'])"/>
                                      </div>
                                    </div>
                                  </div>
                                </el-card>
                              </div>
                            </div>
                          </el-collapse-item>
                        </el-collapse>
                      </div>
                    </el-collapse-item>
                  </el-collapse>
                </div>
              </el-scrollbar>
            </el-tab-pane>
          </el-tabs>
        </div>
      </div>
    </el-checkbox-group>
  </el-dialog>
</template>

<script setup>
import {onMounted, onBeforeUnmount, ref} from "vue";
import {ElMessage, ElText} from "element-plus";
import {DocumentCopy, Download as DownloadIcon} from "@element-plus/icons-vue";
import LazyImage from '@/view/custom/LazyImage.vue';
import {proxyImage} from "@/js/global.js";
import * as http from "@/js/http.js";
import {
  readMikanSearchCache,
  readSeasonCache,
  writeMikanSearchCache,
  writeSeasonCache
} from "./seasonCatalogCache.js";
import {registerMikanCacheScheduler} from './mikanCacheScheduler.js';
import {
  buildAddDraft,
  buildRegexList,
  copyText,
  createSubgroupLoader,
  sourceAdapters,
  submitBatch
} from '@/js/sourceBrowsing.js';
import {createCatalogRequest} from '@/js/catalogRequest.js';

const mikanAdapter = sourceAdapters.mikan

// 批量添加订阅
let rssList = ref([]);

let groupLoading = ref(false)
let activeName = ref("")
let dialogVisible = ref(false)
let loading = ref(false)
let lastRequest = null
let data = ref({
  'seasons': [],
  'items': [],
  'weeks': []
})

let seasonSelect = ref('')

let show = (ani) => {
  seasonSelect.value = ''
  dialogVisible.value = true
  text.value = ''
  data.value = {
    'seasons': [],
    'items': [],
    'weeks': []
  }
  rssList.value = []
  searchAni(ani)
  list(text.value)
}

let searchAni = ani => {
  if (!ani) {
    return
  }

  if (ani.url) {
    let url = new URL(ani.url);
    let searchParams = url.searchParams;
    let mikanId = searchParams.get("bangumiId");
    if (mikanId) {
      text.value = `id: ${mikanId}`
      return
    }
  }

  let title = ani.mikanTitle ? ani.mikanTitle : ani.title
  title = title.replace(/ ?\((19|20)\d{2}\)/g, "").trim()
  title = title.replace(/ ?\[tmdbid=(\d+)]/g, "").trim()
  if (title.length > 2) {
    text.value = title
  }
}

let text = ref('')

let searchLoading = ref(false)
const SEASON_CACHE_TTL = 7 * 24 * 60 * 60 * 1000
const SEARCH_CACHE_TTL = 30 * 60 * 1000

// 搜索与季度目录共用同一份缓存决策（命中/过期后台刷新/失败回退/旧响应丢弃），
// 新鲜窗口按调用给：搜索 30 分钟、季度 7 天。
const catalogRequest = createCatalogRequest({ttl: SEASON_CACHE_TTL})

let search = () => {
  if (text.value.length === 1) {
    ElMessage.error("搜索最少需要两个字符")
    return
  }
  searchLoading.value = true
  list(text.value).finally(() => {
    searchLoading.value = false
  })
}

const applyData = response => {
  const seasons = Array.isArray(response?.seasons) ? response.seasons : []
  const weeks = Array.isArray(response?.weeks) ? response.weeks : []
  if (seasons.length) {
    data.value.seasons = seasons
  }
  data.value.weeks = weeks
  if (weeks.length) {
    activeName.value = weeks[0].weekLabel
  }
  if (!seasonSelect.value) {
    const selected = data.value.seasons.find(item => item.select)
    if (selected) seasonSelect.value = selected.seasonLabel
  }
}

const list = async (query = '', body = {}, options = {}) => {
  const normalizedText = String(query || '').trim()
  const normalizedBody = body && typeof body === 'object' ? {...body} : {}
  const background = options.background === true
  const force = options.force === true
  lastRequest = {text: normalizedText, body: normalizedBody}

  const isSearch = normalizedText !== ''
  const season = normalizedBody?.seasonLabel
      || (normalizedBody?.year && normalizedBody?.season
          ? `${normalizedBody.year} ${normalizedBody.season}`
          : 'current')
  const key = isSearch ? normalizedText : season
  const showLoading = !background
  if (showLoading) loading.value = true
  try {
    const result = await catalogRequest.load({
      key,
      force,
      ttl: isSearch ? SEARCH_CACHE_TTL : SEASON_CACHE_TTL,
      read: () => isSearch ? readMikanSearchCache(key) : readSeasonCache('mikan', key),
      write: (writeKey, data) => {
        if (isSearch) writeMikanSearchCache(writeKey, data)
        else writeSeasonCache('mikan', writeKey, data)
        return Date.now()
      },
      fetch: async () => (await http.mikan(normalizedText, normalizedBody)).data
          || {seasons: [], weeks: [], totalItems: 0}
    })
    // 被更新的请求取代：丢弃，避免旧搜索词/旧季度的数据覆盖当前视图。
    if (result.stale) return null
    const response = result.data || {seasons: [], weeks: [], totalItems: 0}
    applyData(response)
    // module 在请求失败时会回退缓存并带 error 返回（不抛错），这里负责提示。
    if (result.error) {
      if (result.source === 'cache') {
        ElMessage.warning('网络请求失败，已显示缓存的 Mikan 数据')
      } else {
        ElMessage.error(result.error?.message || '加载 Mikan 数据失败')
      }
      return response
    }
    if (response.totalItems < 1 && normalizedText) {
      ElMessage.warning("搜索结果为空")
    }
    return response
  } finally {
    if (showLoading) loading.value = false
  }
}

let change = (v) => {
  let body = data.value.seasons.filter(item => item['seasonLabel'] === v)
  if (body.length) {
    list('', body[0])
  }
}

let selectName = ref('')
let groups = ref({})

// 展开字幕组：缓存与 loading 由共享 loader 管，视图只给端点调用。
const loadingGroups = new Set()
const subgroupLoader = createSubgroupLoader({
  load: url => http.mikanGroup(url).then(res => res.data),
  // 并发展开多个字幕组时按 key 计数：一个完成不能把仍在加载的 spinner 关掉。
  onLoading: (value, key) => {
    if (value) loadingGroups.add(key)
    else loadingGroups.delete(key)
    groupLoading.value = loadingGroups.size > 0
  }
})

let collapseChange = async (v) => {
  if (!v) return
  selectName.value = v
  if (subgroupLoader.has(v)) return
  groups.value = {...groups.value, [v]: await subgroupLoader.load(v)}
}


let matchDialogVisible = ref(false)

let addAni = ref({
  'url': '',
  'match': '',
  'group': ''
})

let regexList = ref([])

let callback = v => {
  regexList.value = buildRegexList(v)
  addAni.value = {...addAni.value, ...buildAddDraft(v, mikanAdapter)}
  matchDialogVisible.value = true
}

let showTag = () => {
  return window.innerWidth > 900;
}

let open = url => {
  window.open(url);
}

defineExpose({show})

let unregisterMikanScheduler
onMounted(() => {
  unregisterMikanScheduler = registerMikanCacheScheduler(
      Symbol('mikan-view'),
      () => {
        const request = lastRequest || {text: '', body: {}}
        return list(request.text, request.body, {force: true, background: true})
      }
  )
})
onBeforeUnmount(() => unregisterMikanScheduler?.())

let emit = defineEmits(['callback'])


let batchAdditionNum = ref(0)
let batchAdditionDialogVisible = ref(false)

let batchAddition = async () => {
  batchAdditionNum.value = 0
  batchAdditionDialogVisible.value = true

  try {
    ElMessage.success("添加中....")
    await submitBatch(
        rssList.value.map(item => JSON.parse(item)),
        mikanAdapter,
        {
          resolve: async draft => (await http.rssToAni(draft)).data,
          add: ani => http.addAni(ani),
          onProgress: done => {
            batchAdditionNum.value = done
          }
        }
    )
    ElMessage.success("添加成功")

    setTimeout(() => {
      location.reload()
    }, 1000)
  } catch (e) {
    ElMessage.error(e)
  } finally {
    batchAdditionDialogVisible.value = false
  }
}

let copy = async (v) => {
  await copyText(v)
  ElMessage.success('已复制')
}

let openUrl = (url) => window.open(url)

</script>

<style scoped>
.el-collapse {
  --el-collapse-header-height: 55px;
}

.match-item {
  margin-right: 12px;
  display: inline;
}

.tag-margin {
  margin-right: 4px;
}

.dialog-footer {
  display: flex;
  width: 100%;
  justify-content: end;
}

.content-wrapper {
  min-height: 300px;
}

.search-section {
  margin: 4px;
}

.search-header {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.season-selector {
  margin-top: 8px;
  width: 100%;
  justify-content: space-between;
}

.season-select {
  max-width: 140px;
}

.scroll-container {
  margin: 8px 0 4px 0;
  height: 600px;
}

.week-tabs {
  margin: 0 4px;
}

.collapse-content {
  margin-left: 15px;
}

.collapse-title {
  align-items: center;
}

.title-text {
  margin-left: 6px;
  line-height: 1.6;
  font-weight: bold;
}

.score-margin {
  margin-left: 4px;
}

.score-color {
  color: #E800A4;
}

.badge-margin {
  margin-left: 4px;
}

.group-content {
  margin-left: 15px;
  min-height: 50px;
}

.group-title-wrapper {
  width: 100%;
  display: flex;
  justify-content: space-between;
}

.group-checkbox-wrapper {
  height: 100%;
}

.checkbox-margin {
  margin-right: 8px;
}

.group-label {
  display: flex;
  align-items: center;
  flex: 1;
  text-align: start;
}

.group-action {
  display: flex;
  align-items: center;
  margin-right: 14px;
  margin-left: 4px;
}

.group-items {
  margin-left: 15px;
}

.item-margin {
  margin-bottom: 4px;
}

.item-footer {
  width: 100%;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.cover {
  border-radius: var(--el-border-radius-base);
  cursor: pointer;
  width: 45px;
  height: 45px;
  object-fit: cover;
  flex-shrink: 0;
}

.match-content {
  max-width: 500px;
  min-width: 200px;
  margin-bottom: 4px;
}
</style>
