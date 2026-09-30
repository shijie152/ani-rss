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
  <el-dialog v-model="dialogVisible" center title="AnimeGarden">
    <el-checkbox-group v-model="rssList">
      <div class="content-wrapper">
        <div class="search-section">
          <div class="flex season-selector">
            <el-button :disabled="rssList.length < 1" bg icon="Plus" text @click="batchAddition">批量添加</el-button>
          </div>
        </div>
        <el-empty v-if="!loading && loadError" :description="loadError">
          <el-button type="primary" @click="list(lastQuery)">重试</el-button>
        </el-empty>
        <div v-else v-loading="loading" class="scroll-container">
          <el-tabs v-model="activeName" class="week-tabs">
            <el-tab-pane v-for="item in data.items" :key="item.weekLabel"
                         :label="item.weekLabel" :name="item.weekLabel" lazy>
              <el-scrollbar class="week-pane-scrollbar">
                <div class="collapse-content">
                  <el-collapse accordion @change="collapseChange">
                    <el-collapse-item v-for="anime in item.subjects" :name="anime.id">
                      <template #title>
                        <div class="flex collapse-title">
                          <LazyImage :src="proxyImage(anime['cover'])" :alt="anime.name" class-name="cover" v-if="anime.cover"
                                     @click.stop="open(`https://animes.garden/subject/${anime.id}`)"/>
                          <div class="flex collapse-title">
                            <el-text :truncated="false" line-clamp="1" size="small"
                                     class="title-text">
                              {{ anime.name }}
                            </el-text>
                          </div>
                          <div v-if="anime['score'] > 0" class="score-margin">
                            <h4 class="score-color">
                              {{ anime['score'].toFixed(1) }}
                            </h4>
                          </div>
                          <el-badge v-if="anime['exists']" class="item badge-margin" type="primary"
                                    value="已订阅"/>
                        </div>
                      </template>
                      <div v-if="selectName === anime.id" v-loading="groupLoading"
                           class="group-content">
                        <el-collapse accordion>
                          <el-collapse-item v-for="group in groups[anime.id]">
                            <template #title>
                              <div class="group-title-wrapper">
                                <div class="group-checkbox-wrapper">
                                  <el-checkbox :value="JSON.stringify(group)" class="checkbox-margin" @click.stop/>
                                </div>
                                <div class="group-label">
                                  <el-text style="max-width: 100px;" truncated>{{ group.name }}</el-text>
                                  &nbsp;
                                  <el-text class="mx-1" size="small">
                                    {{ fromNow(group['lastUpdatedAt'], 'MM/DD/YYYY') }}
                                  </el-text>
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
                                        <el-button :icon="DocumentCopy" bg text @click="copy(ti['magnet']  )"/>
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
import {ref} from "vue";
import {ElMessage, ElText} from "element-plus";
import {DocumentCopy} from "@element-plus/icons-vue";
import LazyImage from '@/view/custom/LazyImage.vue';
import * as http from "@/js/http.js";
import {proxyImage} from "@/js/global.js";
import {
  buildAddDraft,
  buildRegexList,
  copyText,
  createSubgroupLoader,
  sourceAdapters,
  submitBatch
} from '@/js/sourceBrowsing.js';

const animeGardenAdapter = sourceAdapters['anime-garden']
import {fromNow} from "@/js/format.js";

// 批量添加订阅
let rssList = ref([]);

let groupLoading = ref(false)
let activeName = ref("")
let dialogVisible = ref(false)
let loading = ref(false)
let data = ref({
  'items': []
})

let show = (bgmUrl = '') => {
  dialogVisible.value = true
  data.value = {
    'items': []
  }
  rssList.value = []
  list(bgmUrl)
}

let list = async (bgmUrl = '') => {
  loading.value = true
  loadError.value = ''
  lastQuery = bgmUrl
  return http.animeGardenList(bgmUrl)
      .then(res => {
        let items = res.data;

        if (!items || items.length < 1) {
          ElMessage.warning("搜索结果为空")
        }

        data.value.items = items || []
        if (data.value.items.length) {
          activeName.value = items[0].weekLabel
        }
      })
      .catch(e => {
        // 失败要留在页面上：只弹 toast 会转瞬即逝，用户看到的是空白面板。
        loadError.value = '目录数据加载失败，请检查网络或代理设置后重试'
        data.value.items = []
        ElMessage.error(e?.message || '加载 AnimeGarden 数据失败')
      })
      .finally(() => {
        loading.value = false
      });
}

let selectName = ref('')
const loadError = ref('')
// 重试要重放同一个查询：show(bgmUrl) 与添加流程的 list() 参数不同。
let lastQuery = ''
let groups = ref({})

// 展开字幕组：缓存与 loading 由共享 loader 管，视图只给端点调用。
const loadingGroups = new Set()
const subgroupLoader = createSubgroupLoader({
  load: url => http.animeGardenGroup(url).then(res => res.data),
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
  'bgmUrl': '',
  'url': '',
  'match': '',
  'group': ''
})

let regexList = ref([])

let callback = v => {
  regexList.value = buildRegexList(v)
  addAni.value = {...addAni.value, ...buildAddDraft(v, animeGardenAdapter)}
  matchDialogVisible.value = true
}


let showTag = () => {
  return window.innerWidth > 900;
}

let open = url => {
  window.open(url);
}

defineExpose({show})

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
        animeGardenAdapter,
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

.season-selector {
  margin-top: 8px;
  width: 100%;
  justify-content: flex-end;
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
