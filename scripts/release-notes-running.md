
---

### Running it on Linux

```sh
chmod +x SummaReaderSync-*-x86_64.AppImage
./SummaReaderSync-*-x86_64.AppImage
```

The `chmod` is not optional and not ours: a browser cannot preserve the execute
bit, so without it your file manager offers to *open* the AppImage with
something instead of running it. In a file manager: Properties → Permissions →
**Allow executing file as program**.

One file, no repository and no package manager. It offers once to add itself to
your applications menu, and it updates itself from here.
