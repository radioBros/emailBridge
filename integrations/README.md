# EmailBridge automation packages (P8.2)

| Folder | Platform |
| --- | --- |
| `n8n/` | n8n community node |
| `zapier/` | Zapier CLI app |
| `make/` | Make.com app definition (`app.json`) |
| `activepieces/` | Activepieces piece |

Each package talks to the EmailBridge public API: REST-hook triggers create/delete webhook
endpoints; actions cover send / list / search. Tests use mocked HTTP only.
