import type { Component } from 'vue'
import type { RouteRecordRaw } from 'vue-router'

export interface NavigationItem {
  label: string
  to: string
  icon: Component
}

export interface ModuleManifest {
  id: string
  label: string
  description: string
  icon: Component
  navigation: NavigationItem[]
  routes: RouteRecordRaw[]
}

export interface APIEnvelope<T> {
  ok: boolean
  data: T
  error: { code: string; message: string } | null
  meta: { service: string; version: string; requestId: string | null }
}
