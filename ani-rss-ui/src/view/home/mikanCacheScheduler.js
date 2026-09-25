const nextRefreshDelay = nowValue => {
  const now = new Date(nowValue())
  const next = new Date(now)
  next.setHours(3, 0, 0, 0)
  if (next <= now) next.setDate(next.getDate() + 1)
  return next.getTime() - now.getTime()
}

export const createMikanCacheScheduler = ({
  now = () => new Date(),
  scheduleTimer = (callback, delay) => window.setTimeout(callback, delay),
  cancelTimer = handle => window.clearTimeout(handle)
} = {}) => {
  const listeners = new Map()
  let timer
  let refreshing = false

  const schedule = () => {
    if (timer || listeners.size === 0) return
    timer = scheduleTimer(async () => {
      timer = undefined
      if (listeners.size && !refreshing) {
        refreshing = true
        try {
          // 预热只需要更新共享 localStorage；选择一个已注册实例的最近请求即可，
          // 不能对每个隐藏弹窗分别发起一次网络请求。
          const refresh = listeners.values().next().value
          await refresh?.()
        } catch (_) {
          // 预热失败不影响弹窗使用，下一天继续尝试。
        } finally {
          refreshing = false
        }
      }
      schedule()
    }, nextRefreshDelay(now))
  }

  const register = (owner, refresh) => {
    listeners.set(owner, refresh)
    schedule()
    return () => {
      listeners.delete(owner)
      if (!listeners.size && timer) {
        cancelTimer(timer)
        timer = undefined
      }
    }
  }

  return {register}
}

const defaultScheduler = createMikanCacheScheduler()
export const registerMikanCacheScheduler = (...args) => defaultScheduler.register(...args)
