import {onMounted, onUnmounted} from 'vue'
import {createPollingController} from './pollingController.js'

export {createPollingController} from './pollingController.js'

/**
 * 串行轮询：下一次请求只会在上一次完成后排队，页面隐藏时自动暂停。
 */
export const usePolling = (task, interval, options = {}) => {
  const controller = createPollingController(task, interval, options)
  const handleVisibilityChange = () => {
    if (document.hidden) {
      controller.pause()
      return
    }
    controller.resume()
  }

  onMounted(() => document.addEventListener('visibilitychange', handleVisibilityChange))
  onUnmounted(() => {
    controller.stop()
    document.removeEventListener('visibilitychange', handleVisibilityChange)
  })
  return controller
}
