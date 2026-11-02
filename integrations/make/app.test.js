import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Make app definition', () => {
  it('declares trigger subscribe/unsubscribe and send/list/search actions', () => {
    const app = JSON.parse(readFileSync(new URL('./app.json', import.meta.url), 'utf8'))
    expect(app.name).toBe('emailbridge')
    const ids = app.modules.map((m) => m.id)
    expect(ids).toEqual(expect.arrayContaining([
      'watchEvents',
      'sendMessage',
      'listMessages',
      'searchMessages',
    ]))
    const trigger = app.modules.find((m) => m.id === 'watchEvents')
    expect(trigger.webhook.subscribe.url).toBe('/v1/webhook-endpoints')
    expect(trigger.webhook.unsubscribe.method).toBe('DELETE')
  })
})
