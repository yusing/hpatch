Create a new Go CLI project named `ledgerroll` in this empty workspace. Only automated end-to-end behavior is graded. Choose any implementation, dependencies, packages, and source layout that satisfy the command-line contract.

The project must build from the workspace root with:

```
go build -o ledgerroll .
```

The resulting binary must work when run outside its source directory.

## Command line

The supported invocation is:

```
ledgerroll summarize [--from TIME] [--to TIME]
```

Each option may use either `--name VALUE` or `--name=VALUE`, may appear in either order, and may appear at most once. `TIME` must be RFC 3339. A supplied `--from` is inclusive and a supplied `--to` is exclusive. If both are supplied, `from` must be earlier than `to`.

For an invalid `--from` value, write `invalid --from: VALUE` followed by a newline to standard error and exit 2. Treat `--to` the same way. For a non-increasing range, write `invalid time range` followed by a newline to standard error and exit 2. For every other invalid invocation, write `usage: ledgerroll summarize [--from TIME] [--to TIME]` followed by a newline to standard error and exit 2. These failures write nothing to standard output.

## Input

Read newline-delimited JSON from standard input. Ignore blank or whitespace-only lines, but count every physical line when reporting an error. Every nonblank line must contain exactly one JSON object and no trailing value.

A charge object has exactly these string fields:

```
{"id":"c1","at":"2024-01-01T09:00:00Z","account":"alice","currency":"USD","kind":"charge","amount":"12.30"}
```

A refund has the same fields plus a `ref` field naming its charge, and `kind` is `refund`.

Apply these rules in this order:

1. Scan physical input order. A malformed JSON value reports `invalid JSON`. A record with missing, extra, non-string, or empty `id`, `account`, or `currency` fields reports `invalid fields`. Currency must be exactly three uppercase ASCII letters. A timestamp that is not RFC 3339 reports `invalid timestamp "VALUE"`. Kind must be `charge` or `refund`; otherwise report `invalid kind "VALUE"`. A charge must not have `ref`; a refund must have a nonempty `ref`. Amount must be a positive canonical decimal with exactly two fractional digits: an integer part of `0` or a nonzero digit followed by digits, then `.`, then two digits. Report a bad amount as `invalid amount "VALUE"`. IDs must be unique; report the second occurrence as `duplicate id "ID"`.
2. Sort valid records by their timestamp instant. Preserve physical input order when instants are equal.
3. Validate each refund in that order. Its referenced charge must already have been processed; otherwise report `refund ref "REF" not found`. The refund account and currency must equal the charge account and currency; otherwise report `refund "ID" does not match charge "REF"`. Cumulative refunds for one charge must not exceed its amount; otherwise report `refunds for "REF" exceed charge`.

Every record failure writes `invalid record at line N: REASON` followed by a newline to standard error, writes nothing to standard output, and exits 2. Validate the complete input and all refund relationships even when a failing record falls outside the requested reporting window.

Amounts are exact decimal values. Inputs can exceed IEEE-754 or 64-bit integer precision and must not lose precision.

## Output

After validation, include records whose timestamp is within the requested half-open time window. A charge adds its amount and a refund subtracts its amount. Group included records by exact account and currency. Include a group whenever at least one record in that group is in the window, even if its result is zero or negative.

Write one compact JSON object followed by a newline. The object and each total use this exact field order:

```
{"from":null,"to":null,"transactions":2,"totals":[{"account":"alice","currency":"USD","amount":"10.00"}]}
```

`from` and `to` are JSON null when omitted. Otherwise they are strings normalized to UTC using RFC 3339. `transactions` is the number of included records. Sort `totals` by account, then currency, using bytewise lexicographic order. Format every amount with exactly two fractional digits. On success write nothing to standard error and exit 0.
