import type { HealthInfo, PrecheckResult, ProxyDetail, ProxyInput, ProxyView } from './types'

const TOKEN_KEY = 'socat-webui-token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

export class ApiError extends Error {
  code: string
  status: number

  constructor(code: string, status: number, message: string) {
    super(message)
    this.code = code
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = getToken()
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  let resp: Response
  try {
    resp = await fetch(`/api${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError('network', 0, '网络异常，无法连接后端')
  }
  if (resp.status === 401) {
    clearToken()
    window.dispatchEvent(new Event('auth-expired'))
    throw new ApiError('unauthorized', 401, '未登录或会话已过期')
  }
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>
  if (!resp.ok) {
    throw new ApiError(String(data.code ?? 'error'), resp.status, String(data.message ?? `请求失败（${resp.status}）`))
  }
  return data as T
}

export const api = {
  login: (password: string) => request<{ token: string }>('POST', '/auth/login', { password }),
  logout: () => request<Record<string, never>>('POST', '/auth/logout'),
  health: () => request<HealthInfo>('GET', '/health'),
  listProxies: () => request<ProxyView[]>('GET', '/proxies'),
  getProxy: (id: number) => request<ProxyDetail>('GET', `/proxies/${id}`),
  createProxy: (input: ProxyInput, enabled: boolean) =>
    request<ProxyView>('POST', '/proxies', { ...input, enabled }),
  updateProxy: (id: number, input: ProxyInput) => request<ProxyView>('PUT', `/proxies/${id}`, input),
  enableProxy: (id: number) => request<ProxyView>('POST', `/proxies/${id}/enable`),
  disableProxy: (id: number) => request<ProxyView>('POST', `/proxies/${id}/disable`),
  deleteProxy: (id: number) => request<Record<string, never>>('DELETE', `/proxies/${id}`),
  precheck: (input: ProxyInput) => request<PrecheckResult>('POST', '/proxies/precheck', input),
}
