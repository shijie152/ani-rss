import {ElMessage} from "element-plus";
import {authorization} from "@/js/global.js";
import {requestKey, shouldDedupe} from './requestUtils.js';

const REQUEST_TIMEOUT = 30_000
const inflight = new Map()

export class ApiError extends Error {
    constructor(message, {code = 0, status = 0, cause} = {}) {
        super(message)
        this.name = 'ApiError'
        this.code = code
        this.status = status
        this.cause = cause
    }
}

const makeSignal = (externalSignal, timeout) => {
    const controller = new AbortController()
    let timer
    const abort = () => controller.abort(externalSignal?.reason)
    if (externalSignal) {
        if (externalSignal.aborted) abort()
        else externalSignal.addEventListener('abort', abort, {once: true})
    }
    // timeout 为 0 表示不设超时（上传大文件用），负值同样按不设处理。
    if (timeout > 0) {
        timer = window.setTimeout(() => controller.abort(new DOMException('请求超时', 'TimeoutError')), timeout)
    }
    return {
        signal: controller.signal,
        cleanup: () => {
            window.clearTimeout(timer)
            externalSignal?.removeEventListener('abort', abort)
        }
    }
}

let post = (url, body, options = {}) => fetch_(url, 'POST', body, options)
let get = (url, options = {}) => fetch_(url, 'GET', '', options)
let del = (url, body, options = {}) => fetch_(url, 'DELETE', body, options)
let put = (url, body, options = {}) => fetch_(url, 'PUT', body, options)

const fetch_ = (url, method, body, options = {}) => {
    const dedupe = shouldDedupe(url, method, options)
    const key = requestKey(url, method, body)
    if (dedupe && inflight.has(key)) return inflight.get(key)

    const request = requestImpl(url, method, body, options)
    if (!dedupe) return request
    inflight.set(key, request)
    request.finally(() => {
        if (inflight.get(key) === request) inflight.delete(key)
    }).catch(() => {})
    return request
}

const requestImpl = async (url, method, body, options) => {
    const headers = {...(options.headers || {})}
    if (authorization.value) headers.Authorization = authorization.value
    const hasBody = body !== undefined && body !== null && body !== ''
    const isFormData = typeof FormData !== 'undefined' && body instanceof FormData
    if (hasBody && !isFormData) headers['Content-Type'] = 'application/json'
    const {signal, cleanup} = makeSignal(options.signal, options.timeout ?? REQUEST_TIMEOUT)

    try {
        const response = await fetch(url, {
            method,
            body: !hasBody ? null : isFormData ? body : JSON.stringify(body),
            headers,
            signal
        })
        const raw = await response.text()
        let result
        try {
            result = raw ? JSON.parse(raw) : {}
        } catch (cause) {
            throw new ApiError('服务端返回了无效数据', {status: response.status, cause})
        }

        const {code, message, t} = result
        if (t !== undefined && !checkTimestampRange(t, true)) console.warn('与服务端时差超过30分钟')
        const responseCode = Number(code || response.status)
        if (response.ok && responseCode >= 200 && responseCode < 300) return result

        const error = new ApiError(message || `请求失败（${response.status}）`, {
            code: responseCode,
            status: response.status
        })
        if (!options.silent) ElMessage.error(error.message)
        if (responseCode === 403) {
            authorization.value = ''
            window.setTimeout(() => location.reload(), 1000)
        }
        throw error
    } catch (cause) {
        if (cause instanceof ApiError) throw cause
        if (cause?.name === 'TimeoutError' || (cause?.name === 'AbortError' && signal.reason?.name === 'TimeoutError')) {
            throw new ApiError('请求超时，请稍后重试', {cause})
        }
        if (cause?.name === 'AbortError') throw new ApiError('请求已取消', {cause})
        throw new ApiError('网络请求失败，请检查网络或代理设置', {cause})
    } finally {
        cleanup()
    }
}

export const checkTimestampRange = (timestamp, isMilli = true) => {
    const ts = Math.floor(Number(timestamp))
    if (Number.isNaN(ts)) return false
    const targetTime = isMilli ? ts : ts * 1000
    return Math.abs(Date.now() - targetTime) <= 30 * 60 * 1000
}

export default {post, get, del, put}
