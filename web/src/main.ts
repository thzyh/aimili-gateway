import { createApp } from 'vue'

import App from './App.vue'
import { startAppVersionWatcher } from './appVersion'
import router from './router'

startAppVersionWatcher()
createApp(App).use(router).mount('#app')
