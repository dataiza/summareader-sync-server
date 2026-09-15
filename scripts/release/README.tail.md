
## Or as a container

```sh
docker compose up -d
```

The `Dockerfile` here copies the binary beside it rather than compiling
anything, so this needs no Go and no source. It publishes on `127.0.0.1` only;
set `SYNC_BIND` to the address that should reach it from a phone.

## The first device

Once, from a shell, because until one device has a token there is nobody to
authorise the request:

```sh
./summareader-sync first-device "My library" "Desktop" --dir=./pb_data
```

It prints a token **once**, and it is not recoverable — the server keeps only
something to compare against. Paste it into SummaReader on that device.

Every device after the first joins by scanning the pairing code of one that is
already paired. That code carries the key which makes the library readable, and
this server has never held it, cannot hold it, and so cannot issue one.

## Administration

```sh
./summareader-sync devices list
./summareader-sync devices rename <device-id> <name>
./summareader-sync devices revoke <device-id>
./summareader-sync version
```

These speak HTTP to the running server rather than to the database, so there is
never a second writer. They need `SUMMAREADER_OPERATOR_TOKEN` set to whatever
the server was started with.

## Licence

GNU Affero General Public License, version 3. The full text is in `LICENSE`.

Section 13 is the one that matters for a server: run a *modified* version where
other people can reach it over a network, and those people are entitled to its
source. Running an unmodified build from here puts no obligation on you.
