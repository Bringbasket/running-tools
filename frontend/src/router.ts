import { createRouter, createWebHistory } from 'vue-router'
import { modules } from './modules'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/mail/aliases' },
    ...modules.flatMap((module) => module.routes),
    { path: '/:pathMatch(.*)*', redirect: '/mail/aliases' },
  ],
})
