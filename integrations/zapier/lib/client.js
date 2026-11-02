class EmailBridgeClient {
  constructor(auth, request) {
    this.baseUrl = String(auth.baseUrl || '').replace(/\/$/, '')
    this.apiKey = auth.apiKey
    this.request = request
  }

  async call(method, path, body) {
    const res = await this.request({
      method,
      url: `${this.baseUrl}${path}`,
      headers: {
        Authorization: `Bearer ${this.apiKey}`,
        'Content-Type': 'application/json',
      },
      body,
    })
    return typeof res === 'object' && res !== null && 'json' in res && typeof res.json === 'object'
      ? res.json
      : res
  }

  request(method, path, body) {
    return this.call(method, path, body)
  }

  createWebhookEndpoint(input) {
    return this.call('POST', '/v1/webhook-endpoints', input)
  }

  deleteWebhookEndpoint(id) {
    return this.call('DELETE', `/v1/webhook-endpoints/${id}`)
  }

  send(accountId, input) {
    return this.call('POST', `/v1/accounts/${accountId}/messages`, input)
  }

  listMessages(accountId) {
    return this.call('GET', `/v1/accounts/${accountId}/messages`)
  }

  searchMessages(accountId, q) {
    return this.call('GET', `/v1/accounts/${accountId}/messages?q=${encodeURIComponent(q)}`)
  }
}

module.exports = { EmailBridgeClient }
