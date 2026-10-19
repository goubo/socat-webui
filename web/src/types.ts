export type RuntimeState = 'running' | 'stopped' | 'failed' | 'restarting'

export interface RuntimeStatus {
  status: RuntimeState
  detail?: string
  pid?: number
  startedAt?: string
  restartCount: number
}

export interface ProxyView {
  id: number
  name: string
  localPort: number
  remoteHost: string
  remotePort: number
  enabled: boolean
  createdAt: string
  updatedAt: string
  runtime: RuntimeStatus
}

export interface ProxyInput {
  name: string
  localPort: number
  remoteHost: string
  remotePort: number
}

export interface HealthInfo {
  socat: { available: boolean; path: string; version: string; error?: string }
  proxies: { total: number; running: number; failed: number }
}

export interface ProxyEvent {
  id: number
  proxyId: number
  type: string
  detail: string
  createdAt: string
}

export interface PrecheckResult {
  remoteOk: boolean
  remoteErr?: string
  localFree: boolean
  localErr?: string
}

export interface ProxyDetail {
  proxy: ProxyView
  events: ProxyEvent[]
}
