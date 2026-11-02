# Generated OpenAPI document

`openapi.yaml` is the document a booted EmailBridge node serves at `/v1/openapi.yaml`. Every module
registers its huma operations when it starts, so a running node is the complete source of truth. Do not
edit the file by hand.

```bash
task openapi              # boot a full node (needs Docker or Podman), write openapi.yaml, regenerate web types
task gen:web              # only the TypeScript types: web/src/api/schema.d.ts
```

The e2e workflow regenerates the document and fails when the committed file is out of date.
