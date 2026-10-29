import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './style.css'
import { useThemeStore } from './stores/theme'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)

// 主题要在挂载前初始化，否则会有一帧默认配色。
useThemeStore(pinia)

app.use(router)
void router.isReady().then(() => app.mount('#app'))
