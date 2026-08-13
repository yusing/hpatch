Fix `Context.Copy` so a copied Gin context preserves the request's errors and accepted response formats without sharing either slice's mutable backing storage with the original context.

Keep the existing copy behavior for writer state, request data, keys, parameters, handlers, and paths. Preserve `nil` for an unset errors or accepted-formats slice.

Only `context.go` may be changed. Do not modify or add tests, documentation, dependencies, generated files, or any other production path.
