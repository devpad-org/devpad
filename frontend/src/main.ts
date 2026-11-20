import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { reloadOnStaleBuild } from './router/staleBuild'
import './assets/styles/themes.css'
import './assets/styles/main.css'
import { initTheme } from './theme'

initTheme()
reloadOnStaleBuild(router)

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.mount('#app')
