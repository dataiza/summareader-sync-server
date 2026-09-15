/// The version this window reports, shown beside its own name.
///
/// A constant rather than a lookup. Reading pubspec.yaml at runtime means
/// package_info_plus and a platform channel, for a string that is decided at
/// build time — and the drift that a constant risks is exactly what the test
/// beside it refuses.
///
/// Kept in step with pubspec.yaml, and through that with the server it supervises:
/// one release, one number, however many programs it is spread across.
const consoleVersion = '0.3.3';
