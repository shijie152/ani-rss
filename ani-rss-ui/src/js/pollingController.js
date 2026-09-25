/**
 * 无 Vue 依赖的串行轮询控制器，便于在 Node 中做行为和 mutation 测试。
 */
export const createPollingController = (task, interval, {
  immediate = true,
  isHidden = () => document.hidden,
  scheduleTimer = (callback, delay) => window.setTimeout(callback, delay),
  cancelTimer = handle => window.clearTimeout(handle)
} = {}) => {
  let timer
  let running = false
  let wanted = false

  const clear = () => {
    if (timer) cancelTimer(timer)
    timer = undefined
  }

  const schedule = (delay = interval) => {
    clear()
    if (running && wanted && !isHidden()) {
      timer = scheduleTimer(tick, delay)
    }
  }

  const tick = async () => {
    timer = undefined
    if (!running || !wanted || isHidden()) return
    try {
      await task()
    } catch (_) {
      // 业务层负责展示错误；轮询本身必须继续工作。
    } finally {
      schedule(interval)
    }
  }

  const start = () => {
    wanted = true
    if (running) return
    running = true
    if (immediate) tick()
    else schedule()
  }

  const stop = () => {
    wanted = false
    running = false
    clear()
  }

  const pause = () => {
    running = false
    clear()
  }

  const resume = () => {
    if (!wanted || running) return
    running = true
    if (immediate) tick()
    else schedule()
  }

  return {start, stop, pause, resume, run: tick}
}
