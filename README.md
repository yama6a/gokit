# gokit

Shared Go packages for yama6a services.

```bash
go get github.com/yama6a/gokit@latest
```

| Package | What it does |
|---|---|
| `retryhttp` | `http.RoundTripper` that retries by a policy the caller puts on the request context. Jittered backoff or fixed delays. Honors `Retry-After`. |
| `openai` | OpenAI client on `net/http`: Responses API for text, image input and strict JSON schema output, Images API for generate and edit, usage and cost per call. |
| `gemini` | Gemini client on `net/http`: Interactions API for text with JSON schema output and for images with inline references, `store` off, usage and cost per call. |
| `pgretry` | `*sql.DB` on the pgx driver that retries a failed dial for up to 15 s, for example while a CloudNativePG switchover has no primary. Never retries once a statement may have reached the server. |
| `zapx` | zap cores: one redacts secret fields, one mirrors entries into a store such as a database table. |
| `httpserver` | Root mux with `/healthz`, `/readyz` and `/metrics`, request middleware, bearer auth, RFC 9457 problems, server timeouts, graceful shutdown. |
| `secretbox` | AES-256-GCM seal and open with a base64 32-byte key. |

- No package holds global state. Every constructor takes its dependencies, including the logger.
- `openai` and `gemini` do not retry. Give them an `*http.Client` whose transport is `retryhttp`.

## Releases

Every merge to `main` tags a release. A `release:major` or `release:minor` label on the PR picks the bump, patch otherwise.
