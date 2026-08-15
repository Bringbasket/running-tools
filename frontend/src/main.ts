import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { initializeAuth } from './auth'
import './styles.css'

document.documentElement.dataset.theme = localStorage.getItem('running-theme') || 'light'

createApp(App).use(router).mount('#app')
void initializeAuth()
