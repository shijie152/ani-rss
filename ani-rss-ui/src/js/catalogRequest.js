// 目录请求 module：把「先看缓存、过期就后台刷新、失败回退缓存、旧响应丢弃」
// 四条决策收在一处。fetch 与缓存的读写都是注入的 port——缓存的版本号、
// 季节别名等形状留在各自的 cache module 里，这里只做决策。
//
// ponytail: 进程内单实例的序列守卫（每次 load 递增），够用于单页面；
// 若同一 module 要服务多个并发视图，改成按 key 维护序列。
export const createCatalogRequest = ({ttl, now = Date.now, background = task => { void task() }} = {}) => {
  let sequence = 0

  // ttl 可被单次调用覆盖：搜索用 30 分钟、季度目录用 7 天，共用一个 module。
  const load = async ({key, fetch, read, write, force = false, ttl: callTtl} = {}) => {
    const current = ++sequence
    const cached = read ? read(key) : null
    const fresh = cached && now() - cached.savedAt < (callTtl ?? ttl)
    if (!force && fresh) return {data: cached.data, savedAt: cached.savedAt, source: 'cache'}

    const run = async () => {
      try {
        const data = await fetch()
        if (current !== sequence) return {data, stale: true}
        const savedAt = write ? write(key, data) : now()
        return {data, savedAt, source: 'network'}
      } catch (error) {
        if (current !== sequence) return {data: cached?.data ?? null, stale: true, error}
        return {
          data: cached?.data ?? null,
          savedAt: cached?.savedAt,
          source: cached ? 'cache' : 'none',
          error
        }
      }
    }

    if (cached && !force) {
      // 过期但可用：先把旧数据交出去，刷新在后台跑，旧响应由序列守卫丢弃。
      // executor 可注入：生产 fire-and-forget，测试同步执行以便断言刷新结果。
      background(run)
      return {data: cached.data, savedAt: cached.savedAt, source: 'stale'}
    }
    return run()
  }

  return {load}
}
