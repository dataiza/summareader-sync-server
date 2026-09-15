
## Install it as a service

```sh
./install.sh
```

A systemd **user** service — no root, and a unit under `~/.config` you can
inspect and remove yourself. `BIN_DIR`, `DATA_DIR` and `ADDR` override where
the binary, the database and the address go; the defaults are `~/.local/bin`,
`~/.local/share/summareader-sync` and `127.0.0.1:8099`.

`./install.sh --uninstall` reverses it, and keeps the database — every device
token is in there.

A user service stops at logout unless the user lingers. On a box you reach over
ssh that is the whole difference between a server and a program that ran once:

```sh
sudo loginctl enable-linger "$USER"
```
