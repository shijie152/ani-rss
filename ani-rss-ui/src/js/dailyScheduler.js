export const nextDailyRefreshDelay = (
  nowValue = () => new Date(),
  hour = 3,
  minute = 0
) => {
  const now = new Date(nowValue())
  const next = new Date(now)
  next.setHours(hour, minute, 0, 0)
  if (next <= now) next.setDate(next.getDate() + 1)
  return next.getTime() - now.getTime()
}

export const createDailyRefreshRegistry = ({
  now = () => new Date(),
  scheduleTimer = (callback, delay) => window.setTimeout(callback, delay),
  cancelTimer = handle => window.clearTimeout(handle),
  hour = 3,
  minute = 0
} = {}) => {
  let timer
  const listeners = new Map()

  const schedule = () => {
    if (timer !== undefined || listeners.size === 0) return
    timer = scheduleTimer(async () => {
      timer = undefined
      const refreshes = [...listeners.values()]
          .map(group => group.values().next().value)
          .filter(Boolean)
          .map(refresh => Promise.resolve().then(refresh))
      await Promise.allSettled(refreshes)
      schedule()
    }, nextDailyRefreshDelay(now, hour, minute))
  }

  const register = (owner, refresh, group = 'default') => {
    if (!listeners.has(group)) listeners.set(group, new Map())
    listeners.get(group).set(owner, refresh)
    schedule()
    return () => {
      const groupListeners = listeners.get(group)
      groupListeners?.delete(owner)
      if (groupListeners?.size === 0) listeners.delete(group)
      if (listeners.size === 0 && timer !== undefined) {
        cancelTimer(timer)
        timer = undefined
      }
    }
  }

  return {register}
}

const defaultRegistry = createDailyRefreshRegistry()
export const registerDailyRefreshScheduler = (owner, refresh, group) =>
    defaultRegistry.register(owner, refresh, group)
