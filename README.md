# gokit

Shared Go packages for yama6a services.

```bash
go get github.com/yama6a/gokit@latest
```

| Package | What it does |
|---|---|
| `retryhttp` | `http.RoundTripper` that retries by a policy the caller puts on the request context. Honors `Retry-After`. |
| `openai` | OpenAI client on `net/http`: Responses API for text, Images API for generate and edit, usage and cost per call. |
| `zapx` | zap cores: one redacts secret fields, one mirrors entries into a store such as a database table. |
| `httpserver` | Root mux with `/healthz`, `/readyz` and `/metrics`, request middleware, bearer auth, RFC 9457 problems, graceful shutdown. |
| `secretbox` | AES-256-GCM seal and open with a base64 32-byte key. |

- No package holds global state. Every constructor takes its dependencies, including the logger.
- `openai` does not retry. Give it an `*http.Client` whose transport is `retryhttp`.

## Releases

Every merge to `main` tags a release. A `release:major` or `release:minor` label on the PR picks the bump, patch otherwise.
