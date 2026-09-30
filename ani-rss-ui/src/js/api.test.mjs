import test from 'node:test'
import assert from 'node:assert/strict'
import {mkdir, readFile, writeFile} from 'node:fs/promises'
import {resolve} from 'node:path'
import {pathToFileURL} from 'node:url'

// api.js 依赖 Vite 别名与 element-plus，Node 里加载不了原始模块：
// 加载前把这两处 import 换成桩，其余源码保持原样。
const loadApi = async () => {
  const here = resolve(new URL('.', import.meta.url).pathname)
  const source = await readFile(resolve(here, 'api.js'), 'utf8')
  const stub = (text, from, to) => {
    assert.ok(text.includes(from), `api.js 的 import 变了，请更新本测试的加载桩：${from}`)
    return text.replace(from, to)
  }
  const patched = stub(
    stub(
      stub(source, 'import {ElMessage} from "element-plus";', 'const ElMessage = globalThis.__toasts;'),
      'import {authorization} from "@/js/global.js";',
      'const authorization = {value: ""};'
    ),
    "'./requestUtils.js'",
    `'${pathToFileURL(resolve(here, 'requestUtils.js')).href}'`
  )
  const target = resolve(here, '../../../.scratch/.tmp/ui-api/api-under-test.mjs')
  await mkdir(resolve(target, '..'), {recursive: true})
  await writeFile(target, patched)
  return (await import(`${pathToFileURL(target).href}?case=${Date.now()}`)).default
}

const toasts = []
globalThis.__toasts = {error: message => toasts.push(message)}
globalThis.window = {setTimeout, clearTimeout}
globalThis.fetch = async () => ({
  ok: true,
  status: 200,
  text: async () => JSON.stringify({code: 500, message: 'error'})
})

const api = await loadApi()

const status = async options => {
  toasts.length = 0
  const result = await api.post('api/testIpWhitelist', undefined, options).then(() => 'resolved', error => error.message)
  return {result, toasts: [...toasts]}
}

test('failed requests surface a toast by default', async () => {
  assert.deepEqual(await status({}), {result: 'error', toasts: ['error']})
})

test('silent requests reject without a toast', async () => {
  assert.deepEqual(await status({silent: true}), {result: 'error', toasts: []})
})

// 上传大文件走 timeout: 0：不能套用默认 30 秒超时，否则大 zip/媒体会被中断。
test('timeout zero disables the request timeout', async () => {
  let aborted = false
  globalThis.fetch = (_url, init) => new Promise((_resolve, reject) => {
    init.signal.addEventListener('abort', () => {
      aborted = true
      reject(new DOMException('请求超时', 'TimeoutError'))
    })
  })
  const pending = api.post('api/upload', undefined, {timeout: 0, silent: true}).catch(() => 'rejected')
  await new Promise(resolve => setTimeout(resolve, 50))
  assert.equal(aborted, false, 'timeout: 0 must not schedule an abort')
  void pending
})
