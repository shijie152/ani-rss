import {useColorMode, useDark, useDebounceFn, useEventListener, useLocalStorage} from "@vueuse/core";
import {sessionUrl} from "@/js/authenticatedUrl.js";
import {endpointPath} from "@/js/endpoints.js";

/**
 * 保存登录信息
 */
let rememberThePassword = useLocalStorage('rememberThePassword', {
    remember: false,
    username: '',
    password: ''
})

/**
 * 令牌
 */
const authorization = useLocalStorage('authorization', '')

/**
 * 主题管理
 */
const {store} = useColorMode()

/**
 * 最大内容宽度
 */
const maxContentWidth = useLocalStorage('max-content-width', 1600);

/**
 * 显示评分
 */
const showScore = useLocalStorage('show-score', true)

/**
 * 按星期展示
 */
const showWeek = useLocalStorage("show-week", true)

/**
 * 订阅页面布局
 */
const subscriptionViewMode = useLocalStorage('subscription-view-mode', 'cover')

/**
 * 点击订阅封面时执行的操作
 */
const coverClickAction = useLocalStorage('cover-click-action', 'cover')

/**
 * 启动页
 */
const startupPage = useLocalStorage('startup-page', '/home')

/**
 * 显示视频列表
 */
const showPlaylist = useLocalStorage('show-playlist', true)

/**
 * 显示更新时间
 */
const showLastDownloadTime = useLocalStorage("show-last-download-time", true);

/**
 * 强调色
 */
const color = useLocalStorage('--el-color-primary', '#409eff')

/**
 * 改动强调色
 */
const colorChange = (v) => {
    const el = document.documentElement
    el.style.setProperty('--el-color-primary', v)
}

/**
 * 主题初始化
 */
const initTheme = () => {
    /**
     * 夜间模式
     */
    useDark({
        onChanged: dark => {
            // 自动根据夜间模式修改沉浸式状态栏
            const meta = document.getElementById('themeColorMeta');
            meta.content = dark ? '#000000' : '#ffffff';
        }
    })

    // 修改强调色
    colorChange(color.value)
}

/**
 * 布局初始化
 */
const initLayout = () => {
    let app = document.querySelector('#app');

    // 设置最大布局宽度
    maxContentWidth.value = Math.max(maxContentWidth.value, 1200)

    app
        .style.maxWidth = `${maxContentWidth.value}px`

    const el = document.documentElement
    el.style.setProperty('--max-content-width', `${maxContentWidth.value}px`)

}

/**
 * 初始化
 */
const init = () => {
    initTheme()
    initLayout()
}

/**
 * 当页面大小变化时重新计算一下布局
 * 对方法做节流处理
 */
useEventListener(window, 'resize', useDebounceFn(initLayout, 500))

const base64Encode = s => {
    const encoder = new TextEncoder();
    const data = encoder.encode(s);
    return window.btoa(String.fromCharCode(...data));
}

const getBaseUrl = () => {
    const {protocol, host, pathname} = location
    return `${protocol}//${host}${pathname}`
}

const proxyImage = imgUrl => {
    return sessionUrl(endpointPath('proxyImage'), {
        imgUrl: base64Encode(imgUrl),
    }, {base: getBaseUrl(), token: authorization.value})
}

const toApiFile = filename => {
    return sessionUrl(endpointPath('file'), {
        filename: base64Encode(filename),
    }, {base: getBaseUrl(), token: authorization.value})
}

export {
    rememberThePassword,
    authorization,
    store,
    maxContentWidth,
    showScore,
    showWeek,
    subscriptionViewMode,
    coverClickAction,
    startupPage,
    showPlaylist,
    showLastDownloadTime,
    color,
    colorChange,
    init,
    initTheme,
    initLayout,
    base64Encode,
    proxyImage,
    toApiFile,
    getBaseUrl
};
