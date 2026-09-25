import {createApp} from 'vue'
import MainView from '@/view/MainView.vue'
import {
    ArrowDownBold, ArrowLeft, ArrowUpBold, Back, Check, Close, Delete,
    DocumentAdd, DocumentCopy, Download, Edit, Fold, FolderAdd, Grid, Menu,
    MoreFilled, Odometer, Plus, Refresh, RefreshRight, Remove, Right, Search,
    Select, SwitchButton, Tickets, Top, Upload, VideoPlay
} from '@element-plus/icons-vue'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import router from '@/router/index.js'

const app = createApp(MainView)
// 只有通过字符串属性（例如 icon="Refresh"）使用的图标需要全局注册。
// 组件内显式 import 的图标由 Vite 按组件拆分，避免把整个图标包带入入口。
const globalIcons = {
    ArrowDownBold, ArrowLeft, ArrowUpBold, Back, Check, Close, Delete,
    DocumentAdd, DocumentCopy, Download, Edit, Fold, FolderAdd, Grid, Menu,
    MoreFilled, Odometer, Plus, Refresh, RefreshRight, Remove, Right, Search,
    Select, SwitchButton, Tickets, Top, Upload, VideoPlay
}
for (const [key, component] of Object.entries(globalIcons)) {
    app.component(key, component)
}
app.use(router)
app.mount('#app')
