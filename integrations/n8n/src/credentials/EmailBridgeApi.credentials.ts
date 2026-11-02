/** n8n credential shape (simplified; real n8n types are optional peers). */
export class EmailBridgeApi {
  name = 'emailBridgeApi'
  displayName = 'EmailBridge API'
  properties = [
    { displayName: 'Base URL', name: 'baseUrl', type: 'string', default: 'https://emailbridge.example' },
    { displayName: 'API Key', name: 'apiKey', type: 'string', typeOptions: { password: true }, default: '' },
  ]
}
