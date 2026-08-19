# The desktop console

A Flutter window that starts, stops and watches a sync server. It is a
separate program from the server, which stays a plain `CGO_ENABLED=0` Go
build that cross-compiles to five platforms with no C toolchain — a window
needs one, so the window lives here instead.

```sh
flutter run -d linux -- --http=127.0.0.1:8099 --dir=./pb_data
flutter test                    # the suite
go build -o /tmp/summareader-sync ..            # and, with a server to run:
SUMMAREADER_SYNC_BIN=/tmp/summareader-sync flutter test
flutter test --update-goldens --tags golden   # redraws docs/desktop-window.png
```

It runs the server as a child process, with the same argv the systemd unit's
`ExecStart` holds, and reads the counts off that process's `/metrics` and
`/overview` — never by opening the database beside it.

The look comes from `packages/summareader_ui`, which is a copy of the app's
package rather than a dependency. See `VENDORED.md` there.
