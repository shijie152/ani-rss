// 端点表：每个端点的路径、方法与「是否只读」写在一起，请求层从这张表派生
// 去重决策，不再维护第二份手写名单（那正是 a5e96452 里 6 个端点静默失配的原因）。
// path 一律不带前导斜杠，归一化由 requestUtils 负责。
const endpoint = (path, {method = 'POST', readOnly = false} = {}) => ({path, method, readOnly})

export const endpoints = {
  config: endpoint('api/config', {readOnly: true}),
  setConfig: endpoint('api/setConfig'),
  listAni: endpoint('api/listAni', {readOnly: true}),
  addAni: endpoint('api/addAni'),
  setAni: endpoint('api/setAni'),
  deleteAni: endpoint('api/deleteAni'),
  about: endpoint('api/about', {readOnly: true}),
  update: endpoint('api/update'),
  mikan: endpoint('api/mikan', {readOnly: true}),
  mikanGroup: endpoint('api/mikanGroup', {readOnly: true}),
  aniBTGroup: endpoint('api/aniBTGroup', {readOnly: true}),
  animeGardenList: endpoint('api/animeGardenList', {readOnly: true}),
  animeGardenGroup: endpoint('api/animeGardenGroup', {readOnly: true}),
  refreshAll: endpoint('api/refreshAll'),
  refreshAni: endpoint('api/refreshAni'),
  rssToAni: endpoint('api/rssToAni'),
  previewAni: endpoint('api/previewAni'),
  logs: endpoint('api/logs', {readOnly: true}),
  clearLogs: endpoint('api/clearLogs'),
  getThemoviedbName: endpoint('api/getThemoviedbName', {readOnly: true}),
  getThemoviedbGroup: endpoint('api/getThemoviedbGroup', {readOnly: true}),
  testNotification: endpoint('api/testNotification'),
  newNotification: endpoint('api/newNotification'),
  getBgmTitle: endpoint('api/getBgmTitle', {readOnly: true}),
  searchBgm: endpoint('api/searchBgm', {readOnly: true}),
  testProxy: endpoint('api/testProxy'),
  torrentsInfos: endpoint('api/torrentsInfos', {readOnly: true}),
  updateTotalEpisodeNumber: endpoint('api/updateTotalEpisodeNumber'),
  batchScrape: endpoint('api/batchScrape'),
  batchEnable: endpoint('api/batchEnable'),
  importAni: endpoint('api/importAni'),
  stop: endpoint('api/stop'),
  refreshCover: endpoint('api/refreshCover'),
  rate: endpoint('api/rate'),
  setRate: endpoint('api/setRate'),
  downloadPath: endpoint('api/downloadPath'),
  scrape: endpoint('api/scrape'),
  meBgm: endpoint('api/meBgm'),
  trackersUpdate: endpoint('api/trackersUpdate'),
  getEmbyViews: endpoint('api/getEmbyViews'),
  clearCache: endpoint('api/clearCache'),
  downloadLoginTest: endpoint('api/downloadLoginTest'),
  getTgUpdates: endpoint('api/getTgUpdates'),
  login: endpoint('api/login'),
  testIpWhitelist: endpoint('api/testIpWhitelist'),
  playList: endpoint('api/playList', {readOnly: true}),
  getSubtitles: endpoint('api/getSubtitles', {readOnly: true}),
  startCollection: endpoint('api/startCollection'),
  previewCollection: endpoint('api/previewCollection'),
  getCollectionSubgroup: endpoint('api/getCollectionSubgroup'),
  getAniBySubjectId: endpoint('api/getAniBySubjectId', {readOnly: true}),
  aniBT: endpoint('api/aniBT', {readOnly: true}),
  deleteTorrent: endpoint('api/deleteTorrent'),
  ping: endpoint('api/ping', {method: 'GET', readOnly: true})
}

// 不经 http.js 请求函数的端点：BGM OAuth 回调页，以及 UploadView 的多部分上传。
// 路径仍归这张表，调用方按名字取，不再手拼。
export const callbackEndpoints = {
  bgmOAuthCallback: endpoint('api/bgm/oauth/callback'),
  upload: endpoint('api/upload'),
  uploadAndRead: endpoint('api/uploadAndRead'),
  uploadAndReadToBase64: endpoint('api/uploadAndReadToBase64'),
  webuiUpload: endpoint('api/webui/upload'),
  importConfig: endpoint('api/importConfig'),
  // 资源端点：URL 直接进 img/file 标签，不走 http.js 的请求函数。
  proxyImage: endpoint('api/proxyImage', {method: 'GET'}),
  file: endpoint('api/file', {method: 'GET'}),
  // 带凭据的 GET 端点：由 authenticatedUrl 拼 URL，路径仍归这张表。
  exportConfig: endpoint('api/exportConfig', {method: 'GET'}),
  downloadLogs: endpoint('api/downloadLogs', {method: 'GET'}),
  embyWebHook: endpoint('api/embyWebHook', {method: 'GET'}),
  calendarIcs: endpoint('api/calendar.ics', {method: 'GET'})
}

export const readOnlyPaths = new Set(
  Object.values(endpoints).filter(item => item.readOnly).map(item => item.path)
)

// 请求函数用名字取路径，避免在 http.js 里再抄一份字面量清单。
// callbackEndpoints 里的端点由独立回调页调用，同样按名字取，不手拼。
export const endpointPath = name => {
  const found = endpoints[name] || callbackEndpoints[name]
  if (!found) throw new Error(`unknown endpoint: ${name}`)
  return found.path
}
