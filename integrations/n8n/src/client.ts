export type FetchFn = typeof fetch

export type EmailBridgeClientOpts = {
  baseUrl: string
  apiKey: string
  fetch?: FetchFn
}

export class EmailBridgeClient {
  private readonly baseUrl: string
  private readonly apiKey: string
  private readonly fetchFn: FetchFn

  constructor(opts: EmailBridgeClientOpts) {
    this.baseUrl = opts.baseUrl.replace(/\/$/, '')
    this.apiKey = opts.apiKey
    this.fetchFn = opts.fetch ?? fetch
  }

  private headers = (): Record<string, string> => ({
    Authorization: `Bearer ${this.apiKey}`,
    'Content-Type': 'application/json',
    Accept: 'application/json',
  })

  request = async <T>(method: string, path: string, body?: unknown): Promise<T> => {
    const res = await this.fetchFn(`${this.baseUrl}${path}`, {
      method,
      headers: this.headers(),
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    if (!res.ok) {
      const text = await res.text()
      throw new Error(`EmailBridge ${method} ${path}: HTTP ${res.status} ${text}`)
    }
    if (res.status === 204) return undefined as T
    return (await res.json()) as T
  }

  createWebhookEndpoint = (input: {
    name: string
    url: string
    events?: string[]
  }) => this.request<{ id: string }>('POST', '/v1/webhook-endpoints', input)

  deleteWebhookEndpoint = (id: string) =>
    this.request<void>('DELETE', `/v1/webhook-endpoints/${id}`)

  send = (accountId: string, input: { to: string[]; subject: string; text?: string; html?: string }) =>
    this.request<{ id: string }>('POST', `/v1/accounts/${accountId}/messages`, input)

  listMessages = (accountId: string, query = '') =>
    this.request<{ data: unknown[] }>('GET', `/v1/accounts/${accountId}/messages${query}`)

  searchMessages = (accountId: string, q: string) =>
    this.request<{ data: unknown[] }>(
      'GET',
      `/v1/accounts/${accountId}/messages?q=${encodeURIComponent(q)}`,
    )
}
