Optimize etcd's keys-only range path so eligible requests can be answered from
the in-memory MVCC index instead of reading every value from the backend.

Implement the complete production path:

- add an internal fast-keys-only range option carrying keys, create revisions,
  modification revisions, and versions from the index;
- enable it for `RangeRequest.KeysOnly` unless the request sorts by value, since
  value sorting still requires backend values;
- preserve single-key, range-end, requested-revision, deleted-key, and missing-key
  behavior;
- honor limits while scanning the index: without a total-count requirement, stop
  once enough live keys have been collected; with total count enabled, continue
  counting all live keys while retaining only the limited result set;
- return correct key metadata, count, and pagination behavior without fetching
  values from the backend; and
- keep ordinary ranges, count-only ranges, value-sorted keys-only ranges, and
  delete ranges unchanged.

Only these production files may be changed:

- `server/etcdserver/txn/range.go`
- `server/storage/mvcc/index.go`
- `server/storage/mvcc/kv.go`
- `server/storage/mvcc/kvstore_txn.go`

Do not modify or add tests, documentation, generated files, dependencies, or
other production files. Run the focused MVCC and transaction package tests.
