import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

/// Adds a build that has just been uploaded to a TestFlight group, and
/// submits it to Beta App Review.
///
///     dart run tool/testflight_assign.dart \
///       --key AuthKey_ABCD1234EF.p8 --key-id ABCD1234EF --issuer <uuid> \
///       --bundle-id sk.dataiza.summareaderSyncConsole --build 57 \
///       --group 'External testers' \
///       --whats-new notes.md        # optional: "What to Test"
///       --review no                 # optional: an internal group, no review
///
/// Copied from the app repository's tool of the same name, with two changes
/// for this one: `--review no` for the internal group, which needs no Beta App
/// Review and should not spend one of the six a day; and a group that does not
/// exist is a warning, because App Store Connect offers External Testing only
/// once an app has its first build, so the first release here always runs
/// before that group can be made.
///
/// That assignment is the last manual step between a tag and a tester, and
/// App Store Connect has no setting for it. *Enable automatic distribution*
/// exists only on **internal** groups — Apple puts it in the Create New
/// Internal Group dialog and there is no external equivalent — so an external
/// group gets each build by an explicit `POST`, or it gets nothing.
///
/// **Assigning is not submitting.** In the console, adding a build to an
/// external group asks for review in the same click; in the API it does not,
/// and the build sits in the group as "Ready to Submit", invisible to every
/// external tester. So after the group it
/// sets "What to Test" when it is given one, then makes the
/// `betaAppReviewSubmissions` request itself.
///
/// `xcrun altool`, which uploads the package a step earlier, cannot do this:
/// it has no concept of a group.
///
/// **The signature is openssl's and the encoding is ours.** App Store Connect
/// authenticates with an ES256 JWT, and Dart has no ECDSA in its standard
/// library. Shelling out to openssl avoids a dependency the runner would have
/// to keep current; openssl hands back a DER structure, and turning that into
/// the raw `R||S` a JWT wants is the part that is ten lines of unreadable
/// `xxd` in a shell script and ordinary byte handling here. See [derToJose].
///
/// **Nothing here ever prints the key or the token.** The JWT is a bearer
/// credential for the whole account for as long as it lives, so [redact] is
/// run over anything on its way to the log that could have picked one up.
///
/// The exit codes mean something, because the workflow step that calls this
/// is deliberately non-fatal and the log is all anybody will have:
///
/// * 0 — the build is in the group, or already was.
/// * 1 — something unexpected. The message says what.
/// * 2 — the arguments or the credentials are wrong. Nothing was attempted.
/// * 3 — the app, the group, or a processed build could not be found in time.
/// * 4 — Apple refused for one of its two review limits. See [AppleRefusal].
Future<void> main(List<String> args) async {
  try {
    exitCode = await run(args);
  } catch (error, stack) {
    // Nothing thrown here is expected, and an unhandled throw would print a
    // stack trace straight to a log that outlives the run. [redact] is over
    // both because that is the only way to be sure neither carried a
    // credential out with it.
    exitCode = _fail(1, 'unexpected: ${redact('$error')}\n${redact('$stack')}');
  }
}

const _host = 'api.appstoreconnect.apple.com';

/// The body of [main], as a function that returns rather than exits, so it can
/// be driven from somewhere other than a command line.
Future<int> run(List<String> args) async {
  final Map<String, String> options;
  try {
    options = parseArgs(args);
  } on FormatException catch (e) {
    return _fail(2, 'bad arguments: ${redact(e.message)}');
  }

  String? missing;
  String need(String name) {
    final v = options[name];
    if (v == null || v.isEmpty) missing ??= name;
    return v ?? '';
  }

  final keyPath = need('key');
  final keyId = need('key-id');
  final issuerId = need('issuer');
  final bundleId = need('bundle-id');
  final buildNumber = need('build');
  final groupName = need('group');
  final whatsNewPath = options['whats-new'];
  final review = options['review'] != 'no';
  if (missing != null) return _fail(2, 'no --$missing was given.');
  if (!File(keyPath).existsSync()) {
    // The path and not the contents: the path is the thing worth seeing, and
    // the contents are the one thing that must never be printed.
    return _fail(2, 'there is no key at $keyPath.');
  }

  // Generous, because processing is Apple's and takes minutes. The job around
  // this one has its own limit; this exists so that a build stuck in
  // PROCESSING ends in a sentence rather than in a timeout.
  final patience = Duration(
    minutes: int.tryParse(options['wait-minutes'] ?? '') ?? 20,
  );
  final interval = Duration(
    seconds: int.tryParse(options['poll-seconds'] ?? '') ?? 30,
  );

  final api = _Api(keyPath: keyPath, keyId: keyId, issuerId: issuerId);
  try {
    // The app's Apple id is not among the credentials the workflow holds, and
    // the bundle identifier is what the rest of the release already knows.
    final apps = await api.get('/v1/apps', {'filter[bundleId]': bundleId});
    if (apps.status != 200) {
      return _fail(1, 'asking for the app failed: ${apps.summary}');
    }
    final appId = _firstId(apps.json);
    if (appId == null) {
      return _fail(3, 'no app on this account has the bundle id $bundleId.');
    }

    // By name, not by a stored id: a repository variable holding an opaque id
    // nobody can check against the console is a variable that goes stale in
    // silence. Names are visible in both places.
    final groups = await api.get('/v1/betaGroups', {
      'filter[app]': appId,
      'limit': '200',
    });
    if (groups.status != 200) {
      return _fail(1, 'asking for the groups failed: ${groups.summary}');
    }
    final groupId = pickGroup(groups.json, groupName);
    if (groupId == null) {
      return _warn(
        3,
        '$bundleId has no beta group called "$groupName". The groups it does have: ${_groupNames(groups.json)}',
      );
    }

    // ⛔ `version` in this API is the *build* number — 276, not 0.72.0 — and
    // the build does not exist to the API at all until Apple has finished
    // processing it, which is minutes after altool said the upload was done.
    final deadline = DateTime.now().add(patience);
    ({String id, String state})? build;
    while (true) {
      final builds = await api.get('/v1/builds', {
        'filter[app]': appId,
        'filter[version]': buildNumber,
      });
      if (builds.status != 200) {
        return _fail(1, 'asking for the build failed: ${builds.summary}');
      }
      build = findBuild(builds.json, buildNumber);
      if (build != null && build.state == 'VALID') break;
      if (build != null && build.state != 'PROCESSING') {
        return _fail(
          1,
          'build $buildNumber came out of processing as ${build.state}, so there is nothing to give anybody.',
        );
      }
      if (!DateTime.now().isBefore(deadline)) {
        final state = build == null
            ? 'it has not appeared yet'
            : 'it is still ${build.state}';
        return _fail(
          3,
          'build $buildNumber was not ready within ${patience.inMinutes} minutes — $state. The upload itself was fine; assign it by hand once processing finishes.',
        );
      }
      stdout.writeln(
        'build $buildNumber: ${build?.state ?? 'not on App Store Connect yet'} — asking again in ${interval.inSeconds}s',
      );
      await Future<void>.delayed(interval);
    }

    final assigned = await api.post(
      '/v1/betaGroups/$groupId/relationships/builds',
      {
        'data': [
          {'type': 'builds', 'id': build.id},
        ],
      },
    );
    if (assigned.status >= 200 && assigned.status < 300) {
      stdout.writeln('build $buildNumber is now with "$groupName".');
    } else if (classifyRefusal(assigned.status, assigned.body) ==
        AppleRefusal.alreadyAssigned) {
      stdout.writeln('build $buildNumber was already with "$groupName".');
    } else {
      return _refused(assigned, groupName);
    }

    // Before the submission, because a review is of the build as it stands:
    // notes added afterwards still show, but the reviewer reads what was there.
    // A failure here costs the testers the notes, not the build, so it warns
    // and carries on.
    if (whatsNewPath != null &&
        whatsNewPath.isNotEmpty &&
        File(whatsNewPath).existsSync()) {
      final text = whatsNewText(File(whatsNewPath).readAsStringSync());
      if (text.isNotEmpty) {
        final existing = await api.get(
          '/v1/builds/${build.id}/betaBuildLocalizations',
          {},
        );
        final request = whatsNewRequest(
          existing.status == 200 ? existing.json : const {},
          build.id,
          text,
        );
        final written = request.method == 'PATCH'
            ? await api.patch(request.path, request.body)
            : await api.post(request.path, request.body);
        if (written.status < 200 || written.status >= 300) {
          _warn(
            0,
            'could not set What to Test on build $buildNumber: ${written.summary}',
          );
        }
      }
    }

    if (!review) return 0;

    final submitted = await api.post(
      '/v1/betaAppReviewSubmissions',
      reviewSubmission(build.id),
    );
    if (submitted.status >= 200 && submitted.status < 300) {
      stdout.writeln(
        'build $buildNumber is submitted to Beta App Review. External testers get it once Apple approves it.',
      );
      return 0;
    }
    return _refused(submitted, groupName);
  } on SocketException catch (e) {
    return _fail(1, 'could not reach $_host: ${redact(e.message)}');
  } finally {
    api.close();
  }
}

/// Apple's no, to the assignment or to the submission, as an exit code.
int _refused(_Answer answer, String groupName) {
  // Apple has two limits here, both reachable in normal use, and neither is
  // worth retrying into: a retry loop against a 24-hour window is a job
  // that holds a runner for a day and then fails anyway.
  switch (classifyRefusal(answer.status, answer.body)) {
    case AppleRefusal.alreadyAssigned:
      return 0;
    case AppleRefusal.alreadySubmitted:
      stdout.writeln(
        'the build was already submitted to Beta App Review. Nothing to do.',
      );
      return 0;
    case AppleRefusal.versionInReview:
      return _warn(
        4,
        'Apple will only review one build of a version at a time, and another build of this one is in review. This build is uploaded and can be submitted by hand once that clears: ${answer.summary}',
      );
    case AppleRefusal.dailyReviewLimit:
      return _warn(
        4,
        'Apple accepts six builds for TestFlight review in any 24 hours and this would be the seventh. The build is uploaded; submit it by hand tomorrow, or skip it: ${answer.summary}',
      );
    case AppleRefusal.other:
      return _fail(
        1,
        'Apple refused the build for "$groupName": ${answer.summary}',
      );
  }
}

/// The body that asks for Beta App Review of one build.
Map<String, Object?> reviewSubmission(String buildId) => {
  'data': {
    'type': 'betaAppReviewSubmissions',
    'relationships': {
      'build': {
        'data': {'type': 'builds', 'id': buildId},
      },
    },
  },
};

/// Release notes as TestFlight shows them: no Markdown emphasis, and within
/// Apple's 4,000 characters.
String whatsNewText(String notes) {
  final plain = notes.replaceAll('**', '').trim();
  return plain.length <= 4000 ? plain : '${plain.substring(0, 3999)}…';
}

/// "What to Test" for [buildId]: a `PATCH` of its en-US localization when it
/// has one, and a `POST` creating it when it does not. [existing] is the
/// build's `betaBuildLocalizations` listing.
({String method, String path, Map<String, Object?> body}) whatsNewRequest(
  Map<String, dynamic> existing,
  String buildId,
  String text,
) {
  final rows = existing['data'];
  if (rows is List) {
    for (final row in rows) {
      if (row is Map &&
          row['id'] is String &&
          row['attributes'] is Map &&
          row['attributes']['locale'] == 'en-US') {
        final id = row['id'] as String;
        return (
          method: 'PATCH',
          path: '/v1/betaBuildLocalizations/$id',
          body: {
            'data': {
              'type': 'betaBuildLocalizations',
              'id': id,
              'attributes': {'whatsNew': text},
            },
          },
        );
      }
    }
  }
  return (
    method: 'POST',
    path: '/v1/betaBuildLocalizations',
    body: {
      'data': {
        'type': 'betaBuildLocalizations',
        'attributes': {'locale': 'en-US', 'whatsNew': text},
        'relationships': {
          'build': {
            'data': {'type': 'builds', 'id': buildId},
          },
        },
      },
    },
  );
}

/// What Apple said no for, as far as it can be told apart.
///
/// The two limits are matched on Apple's prose because the API gives no
/// machine-readable code for either — every refusal here is
/// `ENTITY_ERROR.RELATIONSHIP.INVALID` or a bare 409. Prose can change, which
/// is why [other] exists and is the default: a refusal reported as a rate
/// limit it is not would send somebody to wait out a day that was never the
/// problem.
enum AppleRefusal {
  /// The build is in the group already, which is this script's own goal.
  alreadyAssigned,

  /// A review submission for this build exists already: a re-run, or a
  /// submission made by hand in the console.
  alreadySubmitted,

  /// Only one build of a version may be in Beta App Review at a time.
  versionInReview,

  /// At most six builds may be submitted for review in 24 hours. Six releases
  /// have happened in one day before, so this one is not hypothetical.
  dailyReviewLimit,

  /// Anything else — an expired key, a rejected entity, a 500.
  other,
}

AppleRefusal classifyRefusal(int status, String body) {
  final text = body.toLowerCase();
  // Before the one-in-review check, whose words ("already", "review") this
  // message also contains.
  if (text.contains('submission already exists') ||
      text.contains('already been submitted') ||
      text.contains('already submitted')) {
    return AppleRefusal.alreadySubmitted;
  }
  if (text.contains('24 hour') ||
      text.contains('24-hour') ||
      text.contains('maximum number of builds')) {
    return AppleRefusal.dailyReviewLimit;
  }
  if (text.contains('already') &&
          (text.contains('review') || text.contains('train')) ||
      text.contains('another build is currently in review') ||
      text.contains('build of this version in beta')) {
    return AppleRefusal.versionInReview;
  }
  if (text.contains('relationship already exists') ||
      text.contains('already assigned') ||
      text.contains('already a member')) {
    return AppleRefusal.alreadyAssigned;
  }
  return AppleRefusal.other;
}

/// The two halves of a JWT that get signed: `base64url(header).base64url(claims)`.
///
/// Apple will not take a token that lives longer than twenty minutes, and the
/// poll above can run for longer than one, so this is called again for every
/// request rather than once for the run.
String signingInput({
  required String keyId,
  required String issuerId,
  required DateTime now,
  Duration lifetime = const Duration(minutes: 10),
}) {
  final issuedAt = now.millisecondsSinceEpoch ~/ 1000;
  final header = {'alg': 'ES256', 'kid': keyId, 'typ': 'JWT'};
  final claims = {
    'iss': issuerId,
    'iat': issuedAt,
    'exp': issuedAt + lifetime.inSeconds,
    'aud': 'appstoreconnect-v1',
  };
  return '${_b64(utf8.encode(jsonEncode(header)))}.${_b64(utf8.encode(jsonEncode(claims)))}';
}

/// A signed ES256 token for the key at [keyPath].
///
/// openssl does the elliptic curve and this does the encoding, which is the
/// division of labour the whole script is built around: no crypto package to
/// keep current, and no DER parsing in a shell.
Future<String> mintToken({
  required String keyPath,
  required String keyId,
  required String issuerId,
  DateTime? now,
}) async {
  final input = signingInput(
    keyId: keyId,
    issuerId: issuerId,
    now: now ?? DateTime.now().toUtc(),
  );
  // Through stdin, not an argument: anything on a command line is visible to
  // every process on the machine, and a runner is a shared machine.
  final openssl = await Process.start('openssl', [
    'dgst',
    '-sha256',
    '-sign',
    keyPath,
    '-binary',
  ]);
  // Both pipes are drained before the exit code is waited on. openssl writes
  // more than a pipe buffer holds only on a bad day, but a deadlock here
  // would hang the job rather than fail it.
  final out = openssl.stdout.fold<BytesBuilder>(
    BytesBuilder(),
    (b, c) => b..add(c),
  );
  final err = openssl.stderr.transform(utf8.decoder).join();
  openssl.stdin.add(utf8.encode(input));
  await openssl.stdin.close();
  final status = await openssl.exitCode;
  if (status != 0) {
    throw StateError(
      'openssl could not sign with the key at $keyPath: ${redact(await err)}',
    );
  }
  return '$input.${_b64(derToJose((await out).takeBytes()))}';
}

/// openssl's DER signature as the raw `R||S` a JWT carries.
///
/// DER writes each half as a signed integer, so a value whose top bit is set
/// gains a leading zero and a value below 2^248 loses a byte. A JWT wants
/// neither: two fixed-width big-endian numbers, 32 bytes each for P-256.
/// Concatenating what DER gave would shift S left in about one signature in
/// 256, and Apple answers a wrong signature with the same 401 it gives a
/// wrong key — so the bug would look like a credentials problem and appear
/// only on some tags.
Uint8List derToJose(List<int> der, {int size = 32}) {
  var at = 0;
  int next() {
    if (at >= der.length) {
      throw const FormatException('the signature ends early');
    }
    return der[at++];
  }

  if (next() != 0x30) {
    throw const FormatException('the signature is not a DER sequence');
  }
  var length = next();
  // A P-256 signature never exceeds 72 bytes, so one extra length byte is as
  // long-form as this ever gets; anything longer is not what openssl wrote.
  if (length == 0x81) length = next();
  if (length != der.length - at) {
    throw const FormatException(
      'the sequence length does not match its contents',
    );
  }

  Uint8List half() {
    if (next() != 0x02) {
      throw const FormatException('the signature is not a pair of integers');
    }
    final count = next();
    if (count == 0 || at + count > der.length) {
      throw const FormatException('an integer runs past the end');
    }
    var value = der.sublist(at, at + count);
    at += count;
    var start = 0;
    while (start < value.length - 1 && value[start] == 0) {
      start++;
    }
    value = value.sublist(start);
    if (value.length > size) {
      throw const FormatException('an integer is too big for this curve');
    }
    return Uint8List(size)..setRange(size - value.length, size, value);
  }

  final r = half();
  final s = half();
  if (at != der.length) {
    throw const FormatException('the signature has bytes after it');
  }
  return Uint8List(size * 2)
    ..setRange(0, size, r)
    ..setRange(size, size * 2, s);
}

/// The build whose `version` is [buildNumber], if App Store Connect has one.
///
/// The request already filters on the same value; this checks it again
/// because the alternative — assigning whatever row came back — is how the
/// wrong build reaches testers, and because a filter Apple silently ignores
/// returns a full list rather than an error.
({String id, String state})? findBuild(
  Map<String, dynamic> body,
  String buildNumber,
) {
  final rows = body['data'];
  if (rows is! List) return null;
  for (final row in rows) {
    if (row is! Map) continue;
    final attributes = row['attributes'];
    final id = row['id'];
    if (attributes is! Map || id is! String) continue;
    if (attributes['version'] != buildNumber) continue;
    final state = attributes['processingState'];
    return (id: id, state: state is String ? state : 'UNKNOWN');
  }
  return null;
}

/// The id of the beta group called [name], matched loosely enough to survive
/// however it was typed into the console.
String? pickGroup(Map<String, dynamic> body, String name) {
  final wanted = name.trim().toLowerCase();
  final rows = body['data'];
  if (rows is! List) return null;
  for (final row in rows) {
    if (row is! Map) continue;
    final attributes = row['attributes'];
    final id = row['id'];
    if (attributes is! Map || id is! String) continue;
    final found = attributes['name'];
    if (found is String && found.trim().toLowerCase() == wanted) return id;
  }
  return null;
}

/// Takes a credential out of text on its way to a log.
///
/// Nothing is supposed to put one there. This is the last line of defence,
/// because a log outlives the run and a token in one is an account.
String redact(String text) => text
    .replaceAll(
      RegExp(r'eyJ[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]+)*'),
      '<redacted token>',
    )
    .replaceAll(
      RegExp('-----BEGIN[^-]*-----[^-]*-----END[^-]*-----'),
      '<redacted key>',
    );

/// `--name value` and `--name=value`, which is every option this takes.
///
/// Not `package:args`: the script is run with `dart run` on a machine that has
/// not resolved this package's dependencies, so it may import nothing but
/// `dart:`.
Map<String, String> parseArgs(List<String> args) {
  final options = <String, String>{};
  for (var i = 0; i < args.length; i++) {
    final arg = args[i];
    if (!arg.startsWith('--')) throw FormatException('$arg is not an option');
    final split = arg.indexOf('=');
    if (split > 2) {
      options[arg.substring(2, split)] = arg.substring(split + 1);
      continue;
    }
    if (i + 1 >= args.length) throw FormatException('$arg was given no value');
    options[arg.substring(2)] = args[++i];
  }
  return options;
}

/// A request and its answer, with the token minted fresh each time.
class _Api {
  _Api({required this.keyPath, required this.keyId, required this.issuerId});

  final String keyPath;
  final String keyId;
  final String issuerId;
  // A request that never answers would hold this job until its own limit
  // and fail the release — which is the one outcome the `continue-on-error`
  // around this step cannot save, since a job timeout is not a step failure.
  // The step upstream needs a whole hand-written watchdog for the same
  // reason; here the client has one.
  final HttpClient _client = HttpClient()
    ..connectionTimeout = const Duration(seconds: 30);

  static const _answerWithin = Duration(seconds: 60);

  Future<_Answer> get(String path, Map<String, String> query) =>
      _send('GET', Uri.https(_host, path, query), null);

  Future<_Answer> post(String path, Map<String, Object?> body) =>
      _send('POST', Uri.https(_host, path), body);

  Future<_Answer> patch(String path, Map<String, Object?> body) =>
      _send('PATCH', Uri.https(_host, path), body);

  Future<_Answer> _send(
    String method,
    Uri url,
    Map<String, Object?>? body,
  ) async {
    final request = await _client.openUrl(method, url);
    final token = await mintToken(
      keyPath: keyPath,
      keyId: keyId,
      issuerId: issuerId,
    );
    request.headers.set(HttpHeaders.authorizationHeader, 'Bearer $token');
    if (body != null) {
      request.headers.contentType = ContentType.json;
      request.write(jsonEncode(body));
    }
    final response = await request.close().timeout(_answerWithin);
    return _Answer(
      response.statusCode,
      await response.transform(utf8.decoder).join().timeout(_answerWithin),
    );
  }

  void close() => _client.close(force: true);
}

class _Answer {
  _Answer(this.status, this.body);

  final int status;
  final String body;

  Map<String, dynamic> get json {
    final decoded = jsonDecode(body);
    return decoded is Map<String, dynamic> ? decoded : const {};
  }

  /// What is safe and useful to print: the status and Apple's own words.
  String get summary {
    final trimmed = body.length > 1200 ? '${body.substring(0, 1200)}…' : body;
    return 'HTTP $status ${redact(trimmed)}';
  }
}

String _b64(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

String? _firstId(Map<String, dynamic> body) {
  final rows = body['data'];
  if (rows is! List || rows.isEmpty) return null;
  final first = rows.first;
  return first is Map && first['id'] is String ? first['id'] as String : null;
}

String _groupNames(Map<String, dynamic> body) {
  final rows = body['data'];
  if (rows is! List) return 'none';
  final names = [
    for (final row in rows)
      if (row is Map &&
          row['attributes'] is Map &&
          row['attributes']['name'] is String)
        '"${row['attributes']['name']}"',
  ];
  return names.isEmpty ? 'none' : names.join(', ');
}

/// A GitHub annotation, which is also a readable sentence on a workstation.
int _fail(int code, String message) {
  stderr.writeln('::error::$message');
  return code;
}

/// For what Apple decided rather than what went wrong: the build is uploaded
/// either way, and a red run for a queue that will clear on its own reads as
/// a broken release.
int _warn(int code, String message) {
  stderr.writeln('::warning::$message');
  return code;
}
