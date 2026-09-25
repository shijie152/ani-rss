import {createRouter, createWebHashHistory} from 'vue-router'
import {startupPage} from '@/js/global.js'

const lazyView = {
    dashboard: () => import('@/view/home/DashboardView.vue'),
    seasons: () => import('@/view/home/SeasonCatalogView.vue'),
    subscriptions: () => import('@/view/home/SubscriptionView.vue'),
    downloads: () => import('@/view/home/TorrentsInfosView.vue'),
    logs: () => import('@/view/home/LogsView.vue'),
    settings: () => import('@/view/home/ConfigView.vue')
}

const startupPaths = ['/home', '/subscriptions']

const routes = [
    {
        path: '/',
        redirect: () => startupPaths.includes(startupPage.value) ? startupPage.value : '/home'
    },
    {
        path: '/home',
        component: lazyView.dashboard
    },
    {
        path: '/seasons',
        component: lazyView.seasons
    },
    {
        path: '/subscriptions',
        component: lazyView.subscriptions
    },
    {
        path: '/downloads',
        component: lazyView.downloads
    },
    {
        path: '/logs',
        component: lazyView.logs
    },
    {
        path: '/settings',
        component: lazyView.settings
    }
]

const router = createRouter({
    history: createWebHashHistory(),
    routes
})

export default router
