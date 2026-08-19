/// How SummaReader looks, without any of what it does.
///
/// The palette, the type styles and the widgets built from them, and nothing
/// that knows about articles, summaries or sync. Three programs draw this
/// look now — the app, and the consoles that supervise the sync server and the
/// MCP mirror — and a look that lives in one of them is a look the other two
/// copy and then diverge from.
library;

export 'src/theme.dart';
export 'src/widgets.dart';
