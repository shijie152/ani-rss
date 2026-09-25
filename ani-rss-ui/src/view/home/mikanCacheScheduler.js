import {createDailyRefreshRegistry, registerDailyRefreshScheduler} from '../../js/dailyScheduler.js'

export const createMikanCacheScheduler = ({
  now = () => new Date(),
  scheduleTimer = (callback, delay) => window.setTimeout(callback, delay),
  cancelTimer = handle => window.clearTimeout(handle)
} = {}) => {
  const registry = createDailyRefreshRegistry({now, scheduleTimer, cancelTimer})
  return {register: (owner, refresh) => registry.register(owner, refresh, 'mikan')}
}

export const registerMikanCacheScheduler = (...args) => registerDailyRefreshScheduler(...args, 'mikan')
