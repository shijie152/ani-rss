<template>
  <SettingsItem label="Api">
    <el-button icon="DocumentCopy" @click="copyEmbyApi">复制 emby 自动点格子 api</el-button>
    <el-button icon="DocumentCopy" @click="copyIcs">复制 ics</el-button>
  </SettingsItem>
  <SettingsItem label="Mikan">
    <el-input v-model:model-value="props.config.mikanHost" placeholder="https://mikanani.me"/>
  </SettingsItem>
  <SettingsItem label="GithubToken">
    <div class="full-width">
      <div>
        <el-input v-model="props.config['githubToken']" clearable placeholder="在此处输入GithubToken"/>
      </div>
      <div style="justify-content: end;" class="flex margin-top-4">
        <el-button :icon="Github" bg
                   @click="openUrl('https://github.com/login/oauth/authorize?client_id=Ov23li1dD89l7iGKhYa3&redirect_uri=https://github-app.wushuo.top/&scope=read:user')">
          获取GithubToken
        </el-button>
      </div>
    </div>
  </SettingsItem>
  <SettingsItem label="最大日志条数">
    <div class="width-150">
      <el-select v-model:model-value="props.config.logsMax">
        <el-option v-for="it in [128,256,512]" :key="it" :label="it" :value="it"/>
      </el-select>
    </div>
  </SettingsItem>
  <SettingsItem label="自动更新">
    <div class="full-width">
      <div>
        <el-switch v-model:model-value="props.config.autoUpdate"/>
      </div>
      <div>
        <el-text class="mx-1" size="small">
          每天 06:00 自动更新程序
        </el-text>
      </div>
    </div>
  </SettingsItem>
  <SettingsItem label="DEBUG">
    <el-switch v-model:model-value="props.config.debug"/>
  </SettingsItem>
  <SettingsItem label="缓存">
    <div class="full-width">
      <div>
        <el-button :loading="clearCacheLoading" bg icon="Delete" @click="clearCache">清理</el-button>
      </div>
      <div>
        <el-text class="mx-1" size="small">
          清理现在不被使用的缓存
        </el-text>
      </div>
    </div>
  </SettingsItem>
  <SettingsItem label="自动备份配置">
    <div>
      <el-switch v-model="props.config['configBackup']"/>
      <br>
      <el-input-number v-model="props.config['configBackupDay']" :min="1">
        <template #suffix>
          <span>天</span>
        </template>
      </el-input-number>
    </div>
  </SettingsItem>
  <SettingsItem label="开机自启">
    <el-switch v-model="props.config['autoStart']"/>
  </SettingsItem>
</template>

<script setup>
import SettingsItem from "@/view/custom/SettingsItem.vue";
import {ElMessage, ElText} from "element-plus";
import {ref} from "vue";
import * as http from "@/js/http.js";
import {Github} from "@vicons/fa";
import {getBaseUrl} from "@/js/global.js";
import {apiKeyUrl} from "@/js/authenticatedUrl.js";
import {copyText} from '@/js/sourceBrowsing.js';

let openUrl = (url) => window.open(url)

let clearCacheLoading = ref(false)
let clearCache = () => {
  clearCacheLoading.value = true
  http.clearCache()
      .then(res => {
        ElMessage.success(res.message);
      })
      .finally(() => {
        clearCacheLoading.value = false
      })
}

let copyEmbyApi = () => {
  let url = apiKeyUrl('api/embyWebHook', {key: props.config.apiKey}, {base: getBaseUrl()});
  copy(url)
}

let copyIcs = () => {
  let url = apiKeyUrl('api/calendar.ics', {key: props.config.apiKey}, {base: getBaseUrl()});
  copy(url)
}

let copy = async (v) => {
  await copyText(v)
  ElMessage.success('已复制')
}

let props = defineProps(['config'])
</script>
