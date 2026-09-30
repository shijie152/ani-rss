import api from "@/js/api.js";
import {withQuery} from "@/js/requestUtils.js";
import {md5} from "js-md5";
import {base64Encode} from "./global.js";
import {endpointPath} from "@/js/endpoints.js";

/**
 * 获取设置
 * @returns {Promise<unknown>}
 */
export let config = () => api.post(endpointPath('config'))

/**
 * 修改设置
 * @param config 设置
 * @returns {Promise<unknown>}
 */
export let setConfig = (config) => api.post(endpointPath('setConfig'), config);

/**
 * 订阅列表
 * @returns {Promise<unknown>}
 */
export let listAni = () => api.post(endpointPath('listAni'))

/**
 * 添加订阅
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let addAni = (ani) => api.post(endpointPath('addAni'), ani)

/**
 * 修改订阅
 * @param move 自动移动本地文件
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let setAni = (move, ani) => api.post(withQuery(endpointPath('setAni'), {move}), ani)

/**
 * 删除订阅
 * @param deleteFiles 同时删除本地文件
 * @param ids ids
 * @returns {Promise<unknown>}
 */
export let deleteAni = (deleteFiles, ids) => api.post(withQuery(endpointPath('deleteAni'), {deleteFiles}), ids)

/**
 * 关于
 * @returns {Promise<unknown>}
 */
export let about = () => api.post(endpointPath('about'))

/**
 * 更新
 * @returns {Promise<unknown>}
 */
export let update = () => api.post(endpointPath('update'))

/**
 * 获取Mikan番剧列表
 * @param text 关键词
 * @param season 季度
 * @returns {Promise<unknown>}
 */
export let mikan = (text, season) => api.post(withQuery(endpointPath('mikan'), {text}), season)

/**
 * 获取Mikan番剧的字幕组列表
 * @param url 番剧url
 * @returns {Promise<unknown>}
 */
export let mikanGroup = (url) => api.post(withQuery(endpointPath('mikanGroup'), {url}))

/**
 * 获取AniBT番剧的字幕组列表
 * @param url 番剧url
 * @returns {Promise<unknown>}
 */
export let aniBTGroup = (url) => api.post(withQuery(endpointPath('aniBTGroup'), {bgmId: url}))

/**
 * 获取AnimeGarden番剧列表
 * @returns {Promise<unknown>}
 */
export let animeGardenList = (bgmUrl) => api.post(withQuery(endpointPath('animeGardenList'), {bgmUrl}))

/**
 * 获取AnimeGarden番剧的字幕组列表
 * @param bgmId 番剧ID
 * @returns {Promise<unknown>}
 */
export let animeGardenGroup = (bgmId) => api.post(withQuery(endpointPath('animeGardenGroup'), {bgmId}))

/**
 * 刷新全部订阅
 * @returns {Promise<unknown>}
 */
export let refreshAll = () => api.post(endpointPath('refreshAll'))

/**
 * 刷新订阅
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let refreshAni = (ani) => api.post(endpointPath('refreshAni'), ani)

/**
 * 将RSS转换为订阅
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let rssToAni = (ani) => api.post(endpointPath('rssToAni'), ani)

/**
 * 预览订阅
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let previewAni = (ani) => api.post(endpointPath('previewAni'), ani)

/**
 * 日志
 * @returns {Promise<unknown>}
 */
export let logs = () => api.post(endpointPath('logs'))

/**
 * 清理日志
 * @returns {Promise<unknown>}
 */
export let clearLogs = () => api.post(endpointPath('clearLogs'))

/**
 * 获取TMDB标题
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let getThemoviedbName = (ani) => api.post(endpointPath('getThemoviedbName'), ani)

/**
 * 获取TMDB剧集组
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let getThemoviedbGroup = (ani) => api.post(endpointPath('getThemoviedbGroup'), ani)

/**
 * 测试通知
 * @param notificationConfig 通知设置
 * @returns {Promise<unknown>}
 */
export let testNotification = (notificationConfig) => api.post(endpointPath('testNotification'), notificationConfig)

/**
 * 新的通知
 * @returns {Promise<unknown>}
 */
export let newNotification = () => api.post(endpointPath('newNotification'))

/**
 * 获取BGM标题
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let getBgmTitle = (ani) => api.post(endpointPath('getBgmTitle'), ani)


/**
 * 搜索BGM条目
 * @param name 关键词
 * @returns {Promise<unknown>}
 */
export let searchBgm = (name) => api.post(withQuery(endpointPath('searchBgm'), {name}))

/**
 * 代理测试
 * @param url url
 * @param config 设置
 * @returns {Promise<unknown>}
 */
export let testProxy = (url, config) => api.post(withQuery(endpointPath('testProxy'), {url}), config)

/**
 * 下载列表
 * @returns {Promise<unknown>}
 */
export let torrentsInfos = () => api.post(endpointPath('torrentsInfos'))

/**
 * 更新总集数
 * @param force 强制
 * @param ids ids
 * @returns {Promise<unknown>}
 */
export let updateTotalEpisodeNumber = (force, ids) => api.post(withQuery(endpointPath('updateTotalEpisodeNumber'), {force}), ids)

/**
 * 批量刮削
 * @param force 强制
 * @param ids ids
 * @returns {Promise<unknown>}
 */
export let batchScrape = (force, ids) => api.post(withQuery(endpointPath('batchScrape'), {force}), ids)

/**
 * 批量 启用/禁用 订阅
 * @param value true/false
 * @param ids ids
 * @returns {Promise<unknown>}
 */
export let batchEnable = (value, ids) => api.post(withQuery(endpointPath('batchEnable'), {value}), ids)

/**
 * 导入订阅
 * @param anis 订阅列表
 * @returns {Promise<unknown>}
 */
export let importAni = (anis) => api.post(endpointPath('importAni'), anis)

/**
 * 停止服务
 * @param status 0:重启 2:关闭
 * @returns {Promise<unknown>}
 */
export let stop = (status) => api.post(withQuery(endpointPath('stop'), {status}))

/**
 * 刷新封面
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let refreshCover = (ani) => api.post(endpointPath('refreshCover'), ani)

/**
 * 获取评分
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let rate = (ani) => api.post(endpointPath('rate'), ani)

/**
 * 进行评分
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let setRate = (ani) => api.post(endpointPath('setRate'), ani)

/**
 * 获取下载位置
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let downloadPath = (ani) => api.post(endpointPath('downloadPath'), ani)

/**
 * 刮削
 * @param force 强制 true/false
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let scrape = (force, ani) => api.post(withQuery(endpointPath('scrape'), {force}), ani)

/**
 * 获取当前BGM账号信息
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let meBgm = (ani) => api.post(endpointPath('meBgm'), ani)

/**
 * 更新trackers
 * @param config 设置
 * @returns {Promise<unknown>}
 */
export let trackersUpdate = (config) => api.post(endpointPath('trackersUpdate'), config)

/**
 * 获取Emby媒体库
 * @param config 设置
 * @returns {Promise<unknown>}
 */
export let getEmbyViews = (config) => api.post(endpointPath('getEmbyViews'), config)

/**
 * 清理缓存
 * @returns {Promise<unknown>}
 */
export let clearCache = () => api.post(endpointPath('clearCache'))

/**
 * 下载器测试
 * @param config 设置
 * @returns {Promise<unknown>}
 */
export let downloadLoginTest = (config) => api.post(endpointPath('downloadLoginTest'), config)

/**
 * 获取TG最近消息
 * @param notificationConfig 通知配置
 * @returns {Promise<unknown>}
 */
export let getTgUpdates = (notificationConfig) => api.post(endpointPath('getTgUpdates'), notificationConfig)

/**
 * 登录
 * @param user
 * @returns {Promise<unknown>}
 */
export let login = (user) => {
    user = JSON.parse(JSON.stringify(user))
    user.password = md5(user.password)
    return api.post(endpointPath('login'), user)
}

/**
 * 测试IP白名单
 * @returns {Promise<Response>}
 */
export let testIpWhitelist = () => api.post(endpointPath('testIpWhitelist'), undefined, {silent: true})

/**
 * 获取视频列表
 * @param ani 订阅
 * @returns {Promise<unknown>}
 */
export let playList = (ani) => api.post(endpointPath('playList'), ani)

/**
 * 获取内封字幕
 * @param filename 视频文件路径
 * @returns {Promise<unknown>}
 */
export let getSubtitles = (filename) => {
    return api.post(withQuery(endpointPath('getSubtitles'), {filename: base64Encode(filename)}));
}

/**
 * 开始下载合集
 * @param info 合集
 * @returns {Promise<unknown>}
 */
export let startCollection = (info) => api.post(endpointPath('startCollection'), info)

/**
 * 预览合集
 * @param info 合集
 * @returns {Promise<unknown>}
 */
export let previewCollection = (info) => api.post(endpointPath('previewCollection'), info)

/**
 * 获取合集字幕组
 * @param info 合集
 * @returns {Promise<unknown>}
 */
export let getCollectionSubgroup = (info) => api.post(endpointPath('getCollectionSubgroup'), info)

/**
 * 将指定id的BGM番剧转换为订阅
 * @param id BGM的ID
 * @returns {Promise<unknown>}
 */
export let getAniBySubjectId = (id) => api.post(withQuery(endpointPath('getAniBySubjectId'), {id}))

/**
 * 获取AniBT番剧列表
 * @param season 季度
 * @param bgmUrl
 * @param text
 * @returns {Promise<unknown>}
 */
export let aniBT = (season, bgmUrl, text) => api.post(endpointPath('aniBT'), {
    season,
    bgmUrl,
    title: text
})

/**
 * 删除缓存的种子
 * @param id 订阅id
 * @param hash 种子hash
 * @returns {Promise<unknown>}
 */
export let deleteTorrent = (id, hash) => api.post(withQuery(endpointPath('deleteTorrent'), {id, hash}))

export let ping = () => api.get(endpointPath('ping'))
