import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import './assets/styles/themes.css'
import './assets/styles/main.css'
import { initTheme } from './theme'

initTheme()

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.mount('#app')
