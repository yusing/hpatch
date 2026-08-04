Implement etcd's server-side RangeStream RPC end to end. The protobuf and generated gRPC surface already contain RangeStream; wire the existing API through the server, clients, and proxy boundaries without changing generated files.

The benchmark workspace is intentionally offline. Use only the supplied base repository, visible task prompt, local toolchain, and visible tests. Do not seek or use oracle revisions, hidden tests, another arm's artifacts, upstream source, commit history, patches, documentation, package networks, or any other external resource. The read-only Go module cache is for compilation only; do not inspect it for task implementation.

Implement the complete production path:

- add the streaming handler to the etcd server and RaftKV interface;
- stream bounded chunks in key order, pin an implicit read revision for the lifetime of the stream, preserve explicit revisions, report More and the total Count on the final chunk, and support CountOnly as one response;
- adapt the chunk limit toward MaxRequestBytes without ever producing an unlimited or zero-sized chunk;
- expose the range Count helper and the ordering/revision-filter predicates needed by the streaming validation;
- reject custom sort orders and revision filters for RangeStream with an Unimplemented gRPC status while preserving normal range validation;
- fill cluster, member, and raft-term header fields on the chunk carrying a response header without overwriting the handler's pinned revision;
- forward RangeStream and its caller options unchanged through the retry KV client without adding a repeatable unary retry policy, and return a clear Unimplemented status from the mock server and gRPC proxy adapter.

Keep normal Range behavior, retry policies, proxy caching, auth checks, and existing non-streaming RPCs unchanged. Preserve context cancellation and propagate Send errors.

Only these production files may be changed:

- `client/v3/mock/mockserver/mockserver.go`
- `client/v3/retry.go`
- `server/etcdserver/api/v3rpc/header.go`
- `server/etcdserver/api/v3rpc/key.go`
- `server/etcdserver/txn/range.go`
- `server/etcdserver/v3_server.go`
- `server/proxy/grpcproxy/adapter/kv_client_adapter.go`
- `server/proxy/grpcproxy/kv.go`

Do not modify or add tests, documentation, generated files, dependencies, or any other production paths. Run the focused RangeStream package tests and keep the implementation confined to the listed files.
