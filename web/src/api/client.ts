/** 极薄的 fetch 封装：统一错误信息与 JSON 解析。 */

export interface ApiError extends Error {
  status: number
}

function makeError(message: string, status: number): ApiError {
  const err = new Error(message) as ApiError
  err.status = status
  return err
}

async function parse(res: Response): Promise<unknown> {
  if (res.status === 204) return null
  const type = res.headers.get('content-type') ?? ''
  if (!type.includes('application/json')) {
    const text = await res.text()
    if (!res.ok) throw makeError(text || res.statusText, res.status)
    return text
  }
  const data = await res.json()
  if (!res.ok) {
    const msg = (data as { error?: string })?.error ?? res.statusText
    throw makeError(msg, res.status)
  }
  return data
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    ...init,
  })
  return (await parse(res)) as T
}

/**
 * 内容库可能与镜像一起更新，必须每次都向服务器确认，不能用磁盘缓存里的旧版本。
 * 服务端用 ETag 协商，内容没变时仍是 304，代价很小。
 */
async function requestFresh<T>(path: string): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    cache: 'no-cache',
  })
  return (await parse(res)) as T
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  getFresh: <T>(path: string) => requestFresh<T>(path),
  put: <T>(path: string, body: unknown) =>
    request<T>(path, { method: 'PUT', body: JSON.stringify(body) }),
  post: <T>(path: string, body: unknown) =>
    request<T>(path, { method: 'POST', body: JSON.stringify(body) }),
  del: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
