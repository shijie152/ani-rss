import {createApp} from 'vue'
import AppView from './AppView.vue'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'

const app = createApp(AppView)
app.mount('#app')
