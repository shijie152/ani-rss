import test from 'node:test'
import assert from 'node:assert/strict'
import {readdir, readFile} from 'node:fs/promises'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {shouldDedupe, withQuery} from './requestUtils.js'
import {endpointPath, endpoints, readOnlyPaths} from './endpoints.js'

test('every endpoint declares a path, method and read-only flag', () => {
  const entries = Object.entries(endpoints)
  assert.ok(entries.length > 40, `expected the full endpoint table, got ${entries.length}`)
  for (const [name, endpoint] of entries) {
    assert.equal(typeof endpoint.path, 'string', `${name}.path`)
    assert.ok(endpoint.path.length > 0, `${name}.path`)
    assert.ok(['GET', 'POST', 'PUT', 'DELETE'].includes(endpoint.method), `${name}.method`)
    assert.equal(typeof endpoint.readOnly, 'boolean', `${name}.readOnly`)
  }
})

test('read-only paths are derived from the endpoint table', () => {
  assert.deepEqual(
    [...readOnlyPaths].sort(),
    [...new Set(Object.values(endpoints).filter(endpoint => endpoint.readOnly).map(endpoint => endpoint.path))].sort()
  )
})

// 请求函数必须从这张表取路径：http.js 里再写一遍字面量，就等于又养了
// 一份会漂移的清单（a5e96452 的成因）。
test('request functions take their path from the table', async () => {
  assert.equal(endpointPath('mikan'), 'api/mikan')
  assert.throws(() => endpointPath('nope'), /unknown endpoint/)

  const source = await readFile(new URL('./http.js', import.meta.url), 'utf8')
  const literals = source.match(/api\.(?:post|get|put|del)\(\s*['"]api\//g) || []
  assert.deepEqual(literals, [], 'http.js must not hard-code endpoint paths')
})

// 真值来自重构前那份手写名单（a5e96452 之前 review 过的 19 条）：它是独立
// 记录，不是从表派生出来的，所以能抓住「改声明」这类漂移。
const knownReadOnlyPaths = [
  'api/about', 'api/aniBT', 'api/aniBTGroup', 'api/animeGardenGroup', 'api/animeGardenList',
  'api/config', 'api/getAniBySubjectId', 'api/getBgmTitle', 'api/getSubtitles',
  'api/getThemoviedbGroup', 'api/getThemoviedbName', 'api/listAni', 'api/logs',
  'api/mikan', 'api/mikanGroup', 'api/ping', 'api/playList', 'api/searchBgm', 'api/torrentsInfos'
]

test('the declared read-only endpoints match the reviewed list', () => {
  assert.deepEqual([...readOnlyPaths].sort(), knownReadOnlyPaths)
})

test('every endpoint declares the fields the request layer needs', () => {
  for (const [name, endpoint] of Object.entries(endpoints)) {
    assert.ok(endpoint.path.startsWith('api/'), name + '.path')
    assert.ok(['GET', 'POST', 'PUT', 'DELETE'].includes(endpoint.method), name + '.method')
    assert.equal(typeof endpoint.readOnly, 'boolean', name + '.readOnly')
  }
})

// 表与请求函数必须一一对应：漏一个就会在运行时才炸，多一个就是死条目。
// 上传地址必须来自端点表：硬编码 url="api/..." 是绕过表的第二份清单，
// 而 :url 绑定的变量若未定义，Vue 会静默当 undefined（构建不报错）。
test('upload views take their url from the endpoint table', async () => {
  const dir = fileURLToPath(new URL('../view/', import.meta.url))
  const walk = async root => {
    const entries = await readdir(root, {withFileTypes: true})
    const nested = await Promise.all(entries.map(entry => {
      const path = join(root, entry.name)
      if (entry.isDirectory()) return walk(path)
      return entry.name.endsWith('.vue') ? [path] : []
    }))
    return nested.flat()
  }
  const files = await walk(dir)
  const findings = []
  for (const file of files) {
    const source = await readFile(file, 'utf8')
    for (const match of source.matchAll(/url="api\/(\w+)"/g)) {
      findings.push(`${file}: hard-coded url="api/${match[1]}"`)
    }
    for (const match of source.matchAll(/:url="(\w+)"/g)) {
      const defined = new RegExp(`^const ${match[1]} = callbackEndpoints`, 'm')
      if (!defined.test(source)) findings.push(`${file}: ${match[1]} is not defined from the endpoint table`)
    }
  }
  assert.deepEqual(findings, [])
})

// 端点表是路径的唯一来源：模块里再写一份 api/ 字面量，就是会漂移的第二份清单。
// 白名单只有端点表自身与 http.js（http.js 通过 endpointPath 取名字，不含字面量）。
test('no module outside the table hard-codes an api path', async () => {
  const root = fileURLToPath(new URL('./', import.meta.url))
  const walk = async dir => {
    const entries = await readdir(dir, {withFileTypes: true})
    const nested = await Promise.all(entries.map(entry => {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) return walk(path)
      return entry.name.endsWith('.js') ? [path] : []
    }))
    return nested.flat()
  }
  const files = (await walk(root)).filter(file =>
    !file.endsWith('endpoints.js') && !file.endsWith('.test.mjs'))
  const findings = []
  for (const file of files) {
    const source = await readFile(file, 'utf8')
    for (const match of source.matchAll(/['"`]api\/([A-Za-z]+)/g)) {
      findings.push(`${file}: ${match[0]}`)
    }
  }
  assert.deepEqual(findings, [])
})

test('the table and the request functions cover the same endpoints', async () => {
  const source = await readFile(new URL('./http.js', import.meta.url), 'utf8')
  const used = new Set([...source.matchAll(/endpointPath\('([A-Za-z0-9]+)'\)/g)].map(match => match[1]))

  assert.deepEqual([...used].sort(), Object.keys(endpoints).sort())
})
