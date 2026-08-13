Fix `res.send` so an application-provided `Transfer-Encoding` response header is not combined with a generated `Content-Length` header.

Keep normal body conversion, content type, ETag, freshness, status-specific header removal, HEAD handling, and response completion behavior unchanged. The check must work for any existing transfer-coding value.

Only `lib/response.js` may be changed. Do not modify or add tests, documentation, dependencies, generated files, or any other production path.
