import { reactive } from 'vue'

export type ToastTone = 'success' | 'error' | 'warning' | 'info'
export interface ToastItem { id: number; message: string; tone: ToastTone }

export const toastState = reactive({ items: [] as ToastItem[] })
let nextToastID = 1

export function dismissToast(id: number) {
  toastState.items = toastState.items.filter((item) => item.id !== id)
}

export function showToast(message: string, tone: ToastTone = 'success', duration = 3200) {
  const item = { id: nextToastID++, message, tone }
  toastState.items.push(item)
  if (duration > 0) window.setTimeout(() => dismissToast(item.id), duration)
  return item.id
}
