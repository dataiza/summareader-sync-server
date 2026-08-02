# allreader-sync-server

One implementation, two deployments: self-host it for free, or use the hosted
instance. Same binary, different URL — there is no "cloud edition".

That is only sustainable because the server is deliberately incapable. It
stores opaque ciphertext against sequence numbers and never learns what any of
it means. It cannot search, cannot count unread, and cannot render a web
reader, because it holds no keys.

## Running it

```sh
go build -o allreader-sync .
./allreader-sync serve --http=127.0.0.1:8099 --dir=./pb_data
```

## The five operations, frozen

```
POST /append          -> {"seq": n}
GET  /from/{seq}      -> {"entries": [...]}   entries strictly after seq
PUT  /blob/{name}     content-addressed, idempotent
GET  /blob/{name}
GET  /subscribe       -> {"head": n}          a change hint, no payload
```

Plus `GET /instance`, which is not part of the contract. It exists so a client
can tell "different server" from "same server, no data" — without it, pointing
at a fresh instance is indistinguishable from everything having been deleted,
and those want opposite responses.

The contract is frozen on purpose. Adapters stay a few hundred lines each only
while it never grows; adding "query by tag" or "count unread" would double the
work permanently and mean building a database once per backend.

## Why `seq` is a counter row and not an identity column

This is the one non-obvious thing in the server, and it is load-bearing.

The requirement is that `seq` be strictly monotonic per account, gap-free, and
visible to a reader only after commit.

An auto-increment column cannot provide that. Identity values are assigned at
`INSERT`, not at `COMMIT`. With two devices appending at once, a reader can
observe seq 5 while seq 4 is still in flight, advance its cursor past 4, and
never see entry 4 again. Nothing errors. The entry is simply gone from that
device's view, permanently, and only under concurrency — which is to say, only
in production.

The fix is a counter row bumped inside the same transaction as the insert, so
both become visible at the same commit. It serializes appends per account,
which costs nothing when an account has three devices.

`append_test.go` asserts the invariant under concurrent writers.
`naive_check_test.go` implements the wrong version and shows it produces
duplicates — roughly 75 of them per 96 appends here — so the test is known to
be capable of catching the bug rather than merely passing.

## What the server knows

| Knows | Does not know |
|---|---|
| account ids | who the account belongs to |
| device tokens | what any device is called by its owner |
| sequence numbers | what order means to the client |
| ciphertext, by size | anything inside it |
| blob names (HMACs) | whether two accounts hold the same file |

Billing identity maps to `account_id` in a separate table, outside the sync
path.

## Revocation

Dropping a device's token stops it syncing. Whatever it already downloaded
stays readable, because it still holds its own copy of the master key. There is
no key rotation and no re-encryption pass — those would mean re-encrypting the
whole library. The client copy says exactly this and no more.

## Version pinning

PocketBase is pre-1.0 with no compatibility guarantee, so the version in
`go.mod` is exact. Upgrade deliberately and re-run the tests; the collection
API in particular changes between minor versions.
