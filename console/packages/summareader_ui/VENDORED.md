# A copy, not a fork

`lib/` and `pubspec.yaml` here are copied verbatim from the SummaReader app's
own `packages/summareader_ui`. Nothing in this directory is edited here — a
change made in this copy is a change the app never sees, and the point of the
package is that three programs draw one look.

Update it with:

    scripts/sync-ui.sh              # from ../summareader
    scripts/sync-ui.sh /path/to/it  # from somewhere else

which also re-copies the two fonts the package names, into
`console/assets/fonts`.

A copy rather than a `git:` dependency in `pubspec.yaml` because resolving one
means pub reading that repository's metadata, and the credential available
here cannot — CI would need a token nobody has issued. The price is drift, and
`console/test/vendored_ui_test.dart` is what charges it: on any machine that
has the app checked out beside this repository, that test fails as soon as the
two differ. Where the app is not checked out, it skips rather than failing,
because a CI box holding only this repository has nothing to drift from.
