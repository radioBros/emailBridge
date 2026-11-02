export type Auth = { baseUrl: string; apiKey: string }

export const ebFetch = async (
  auth: Auth,
  method: string,
  path: string,
  body?: unknown,
  fetchFn: typeof fetch = fetch,
) => {
  const res = await fetchFn(`${auth.baseUrl.replace(/\/$/, '')}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${auth.apiKey}`,
      'Content-Type': 'application/json',
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  if (res.status === 204) return undefined
  return res.json()
}

export const subscribeTrigger = (auth: Auth, targetUrl: string, fetchFn?: typeof fetch) =>
  ebFetch(
    auth,
    'POST',
    '/v1/webhook-endpoints',
    { name: 'Activepieces EmailBridge', url: targetUrl, events: ['*'] },
    fetchFn,
  )

export const sendMessage = (
  auth: Auth,
  accountId: string,
  input: { to: string[]; subject: string; text?: string },
  fetchFn?: typeof fetch,
) => ebFetch(auth, 'POST', `/v1/accounts/${accountId}/messages`, input, fetchFn)

export const listMessages = (auth: Auth, accountId: string, fetchFn?: typeof fetch) =>
  ebFetch(auth, 'GET', `/v1/accounts/${accountId}/messages`, undefined, fetchFn)

export const searchMessages = (auth: Auth, accountId: string, q: string, fetchFn?: typeof fetch) =>
  ebFetch(
    auth,
    'GET',
    `/v1/accounts/${accountId}/messages?q=${encodeURIComponent(q)}`,
    undefined,
    fetchFn,
  )
