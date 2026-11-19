export type RuntimeState = 'running' | 'stopped' | 'failed' | 'restarting'

export interface VersionInfo {
  version: string
}

export interface RuntimeStatus {
  status: RuntimeState
  detail?: string
  pid?: number
  startedAt?: string
  restartCount: number
}

/** 本地监听协议族：IPv4 / IPv6 / 双栈（v6 监听同时接受 v4 连接） */
export type LocalFamily = 'ipv4' | 'ipv6' | 'dual'

/** 远端解析协议族：自动 / 强制 IPv4 / 强制 IPv6（强制仅对主机名生效） */
export type RemoteFamily = 'auto' | 'ipv4' | 'ipv6'

export interface ProxyView {
  id: number
  name: string
  localPort: number
  localFamily: LocalFamily
  remoteHost: string
  remotePort: number
  remoteFamily: RemoteFamily
  enabled: boolean
  createdAt: string
  updatedAt: string
  runtime: RuntimeStatus
}

export interface ProxyInput {
  name: string
  localPort: number
  localFamily: LocalFamily
  remoteHost: string
  remotePort: number
  remoteFamily: RemoteFamily
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
