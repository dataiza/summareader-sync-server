/// How long ago something was, in the roughest terms that are still true.
///
/// Minutes are the finest resolution the server records, so a seconds figure
/// here would be a precision the data does not have.
String ago(String stamp, {DateTime? now}) {
  if (stamp.isEmpty) return 'never synced';
  final when = DateTime.tryParse(stamp);
  if (when == null) return stamp;

  final since = (now ?? DateTime.now()).difference(when);
  if (since.inMinutes < 2) return 'just now';
  if (since.inHours < 1) return '${since.inMinutes} minutes ago';
  if (since.inHours < 48) return '${since.inHours} hours ago';
  return '${since.inDays} days ago';
}

/// Keeps the pane from saying "1 devices". Two words of English rather than a
/// pluralisation library, because these are the only nouns it counts.
String plural(num n, String noun) {
  if (n == 1) return '1 $noun';
  return '${n.round()} ${noun == 'entry' ? 'entries' : '${noun}s'}';
}

/// A byte count as the megabytes a person reads off a disk.
String megabytes(int bytes) => '${(bytes / (1 << 20)).toStringAsFixed(1)} MB';
