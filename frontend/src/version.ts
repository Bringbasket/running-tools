function buildValue(value: string | undefined, fallback: string) {
  const normalized = value?.trim()
  return normalized || fallback
}

export const APP_VERSION = buildValue(import.meta.env.VITE_APP_VERSION, '0.0.1')
export const APP_REVISION = buildValue(import.meta.env.VITE_APP_REVISION, 'dev')
