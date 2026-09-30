// 带鉴权的下载/资源 URL 只在这里装配：会话 token 走 `s=`，API key 走
// `api-key=`。调用方只给路径与参数，不再自己拼 query（那正是
// ui-contracts 里那条扫源码正则存在的原因）。

const defaultBase = () => {
  const {protocol, host, pathname} = globalThis.location
  return `${protocol}//${host}${pathname}`
}

const build = (path, params, base) => {
  const url = new URL(path, base || defaultBase())
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue
    url.searchParams.set(key, String(value))
  }
  return url.toString()
}

// 会话 token：下载日志、配置文件导出、图片/文件代理。
export const sessionUrl = (path, params = {}, {base, token} = {}) =>
  build(path, {...params, s: token}, base)

// API key：给外部服务调用的入口（Emby webhook、ICS 日历）。
export const apiKeyUrl = (path, {key, ...params} = {}, {base} = {}) =>
  build(path, {...params, 'api-key': key}, base)
