import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import '../tool/testflight_assign.dart';

/// The parts of `tool/testflight_assign.dart` that can be checked without
/// asking Apple anything.
///
/// The script runs once per tag, on a Mac, after a build has already been
/// paid for — the worst place in the repository to find out that a signature
/// was encoded wrongly or that a refusal was read as a success. So everything
/// between the key and the socket is a plain function with a plain answer,
/// and only the four HTTP calls themselves are untested.
void main() {
  group('the token', () {
    test('says what App Store Connect requires and nothing else', () {
      final input = signingInput(
        keyId: 'ABCD1234EF',
        issuerId: '69a6de70-0000-47e3-e053-5b8c7c11a4d1',
        now: DateTime.utc(2026, 10, 1, 12),
      );
      final parts = input.split('.');
      expect(parts, hasLength(2));

      final header = jsonDecode(
        utf8.decode(base64Url.decode(base64.normalize(parts[0]))),
      );
      expect(header, {'alg': 'ES256', 'kid': 'ABCD1234EF', 'typ': 'JWT'});

      final claims = jsonDecode(
        utf8.decode(base64Url.decode(base64.normalize(parts[1]))),
      );
      expect(claims['iss'], '69a6de70-0000-47e3-e053-5b8c7c11a4d1');
      expect(claims['aud'], 'appstoreconnect-v1');
      expect(claims['iat'], 1790856000);
      // Apple refuses a token that lives longer than twenty minutes, and the
      // poll below can run for longer than one, which is why every request
      // mints its own rather than holding one open.
      expect(claims['exp'] - claims['iat'], lessThanOrEqualTo(1200));
      expect(claims['exp'], greaterThan(claims['iat']));
    });

    test(
      'is base64url without padding, which a JWT is not allowed to carry',
      () {
        final input = signingInput(
          keyId: 'A' * 10,
          issuerId: 'i' * 36,
          now: DateTime.utc(2026, 10, 1),
        );
        expect(input, isNot(contains('=')));
        expect(input, isNot(contains('+')));
        expect(input, isNot(contains('/')));
      },
    );
  });

  group('the signature', () {
    // Real answers from `openssl dgst -sha256 -sign key.p8 -binary`, which is
    // what the script shells out to. openssl emits a DER SEQUENCE of two
    // signed integers; a JWT wants the two values raw, fixed at 32 bytes
    // each. The three cases below are the three shapes openssl produces.

    test('drops the sign byte DER adds to a value with its top bit set', () {
      // R is 33 bytes here because it begins 0xd3; S is a plain 32.
      final der = _hex(
        '3045'
        '022100d37b077026ac59c5c6e1386c75e17627f1680cef85e46d63bf506725c4610d00'
        '02201a65c188a1de5fdab18c5e188dfd4108fb351b750861756c0b842dae6ce5bbc4',
      );
      expect(
        _toHex(derToJose(der)),
        'd37b077026ac59c5c6e1386c75e17627f1680cef85e46d63bf506725c4610d00'
        '1a65c188a1de5fdab18c5e188dfd4108fb351b750861756c0b842dae6ce5bbc4',
      );
    });

    test('handles both values carrying one', () {
      final der = _hex(
        '3046'
        '022100c1ed027783fa1e2f3358ec87d6b2e6e8e0949d8f826db74075379a29e35dc156'
        '022100c7c5f3c9efe0b03956a01740f34d1d877f183687d5025d1868a2d0568abbb189',
      );
      expect(
        _toHex(derToJose(der)),
        'c1ed027783fa1e2f3358ec87d6b2e6e8e0949d8f826db74075379a29e35dc156'
        'c7c5f3c9efe0b03956a01740f34d1d877f183687d5025d1868a2d0568abbb189',
      );
    });

    test('handles neither carrying one', () {
      final der = _hex(
        '3045'
        '022074e4d14e19574f1942667b9f6e76937d50aa36f10c2c6153619956e353850527'
        '022100f6da58cfc07f56b7a793ad4f1b6184f0586245d54597c7dd6d4192d7b89e6be1',
      );
      expect(
        _toHex(derToJose(der)),
        '74e4d14e19574f1942667b9f6e76937d50aa36f10c2c6153619956e353850527'
        'f6da58cfc07f56b7a793ad4f1b6184f0586245d54597c7dd6d4192d7b89e6be1',
      );
    });

    test('pads a value DER wrote short back out to 32 bytes', () {
      // One signature in 256 has an R below 2^248, and DER writes it in
      // fewer bytes. Concatenating what DER gave would shift S left and
      // produce a signature Apple rejects with no useful message, which is
      // the bug this whole function exists to avoid.
      final der = _hex(
        '3043'
        '021e0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e'
        '022100ff112233445566778899aabbccddeeff00112233445566778899aabbccddeeff',
      );
      expect(
        _toHex(derToJose(der)),
        '00000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e'
        'ff112233445566778899aabbccddeeff00112233445566778899aabbccddeeff',
      );
    });

    test('refuses anything that is not a pair of integers', () {
      expect(() => derToJose(_hex('3003020101')), throwsFormatException);
      expect(() => derToJose(_hex('3100')), throwsFormatException);
      expect(() => derToJose(_hex('')), throwsFormatException);
      // A sequence whose length disagrees with what follows it.
      expect(() => derToJose(_hex('3045020101')), throwsFormatException);
    });
  });

  group('the whole token', () {
    // The vectors above prove the encoding against answers openssl really
    // gave. They cannot prove that the right bytes were handed to it, and a
    // token signed over the wrong string comes back from Apple as the same
    // 401 a wrong key does — so this signs one for real and asks openssl to
    // verify it, which is the only part of the credential path a machine can
    // check without an Apple account.
    late Directory scratch;
    late File key;

    setUpAll(() {
      scratch = Directory.systemTemp.createTempSync('testflight_assign');
      key = File('${scratch.path}/AuthKey_TEST123456.p8');
      final made = Process.runSync('openssl', [
        'genpkey',
        '-algorithm',
        'EC',
        '-pkeyopt',
        'ec_paramgen_curve:P-256',
        '-out',
        key.path,
      ]);
      expect(
        made.exitCode,
        0,
        reason: 'openssl could not make a test key: ${made.stderr}',
      );
    });

    tearDownAll(() => scratch.deleteSync(recursive: true));

    test('verifies against the key that signed it', () async {
      final token = await mintToken(
        keyPath: key.path,
        keyId: 'TEST123456',
        issuerId: '69a6de70-0000-47e3-e053-5b8c7c11a4d1',
      );
      final parts = token.split('.');
      expect(parts, hasLength(3));

      // Back to DER, because that is the only shape openssl will verify.
      final raw = base64Url.decode(base64.normalize(parts[2]));
      expect(raw, hasLength(64));
      final der = File('${scratch.path}/sig.der')
        ..writeAsBytesSync(_toDer(raw.sublist(0, 32), raw.sublist(32)));
      final signed = File('${scratch.path}/signed')
        ..writeAsStringSync('${parts[0]}.${parts[1]}');
      final pub = File('${scratch.path}/pub.pem');
      expect(
        Process.runSync('openssl', [
          'pkey',
          '-in',
          key.path,
          '-pubout',
          '-out',
          pub.path,
        ]).exitCode,
        0,
      );

      final check = Process.runSync('openssl', [
        'dgst',
        '-sha256',
        '-verify',
        pub.path,
        '-signature',
        der.path,
        signed.path,
      ]);
      expect(check.exitCode, 0, reason: '${check.stdout}${check.stderr}');
    });
  }, skip: _openssl ? null : 'needs openssl on PATH');

  group('finding the build', () {
    // `version` in this API is the build number, not the marketing version,
    // and the two are a digit apart in a log.
    Map<String, dynamic> body(List<Map<String, dynamic>> rows) => {
      'data': rows,
    };
    Map<String, dynamic> row(String id, String version, String state) => {
      'type': 'builds',
      'id': id,
      'attributes': {'version': version, 'processingState': state},
    };

    test('returns the row whose build number matches', () {
      final found = findBuild(
        body([row('b-275', '275', 'VALID'), row('b-276', '276', 'PROCESSING')]),
        '276',
      );
      expect(found?.id, 'b-276');
      expect(found?.state, 'PROCESSING');
    });

    test('is null while the build has not reached the API', () {
      expect(findBuild(body([]), '276'), isNull);
      expect(findBuild(body([row('b-275', '275', 'VALID')]), '276'), isNull);
      expect(findBuild(const {}, '276'), isNull);
    });

    test('does not mistake the marketing version for the build number', () {
      expect(findBuild(body([row('b-276', '276', 'VALID')]), '0.72.0'), isNull);
    });

    test('survives a row shaped differently than expected', () {
      expect(findBuild(const {'data': 'nonsense'}, '276'), isNull);
      expect(
        findBuild(
          body([
            const {'id': 'b-276'},
          ]),
          '276',
        ),
        isNull,
      );
    });
  });

  group('finding the group', () {
    Map<String, dynamic> groups(List<String> names) => {
      'data': [
        for (var i = 0; i < names.length; i++)
          {
            'type': 'betaGroups',
            'id': 'g$i',
            'attributes': {'name': names[i]},
          },
      ],
    };

    test(
      'matches by name, because an id nobody can read goes stale in silence',
      () {
        expect(
          pickGroup(
            groups(['Testers', 'External testers']),
            'External testers',
          ),
          'g1',
        );
      },
    );

    test(
      'forgives the case and the spacing a human typed into the console',
      () {
        expect(
          pickGroup(groups(['  External Testers ']), 'external testers'),
          'g0',
        );
      },
    );

    test('is null when the app has no such group', () {
      expect(pickGroup(groups(['Testers']), 'External testers'), isNull);
      expect(pickGroup(const {'data': []}, 'External testers'), isNull);
    });
  });

  group('reading a refusal', () {
    test('recognises the one-in-review limit', () {
      expect(
        classifyRefusal(
          409,
          '{"errors":[{"status":"409","detail":"There is already a build of this version in beta app review."}]}',
        ),
        AppleRefusal.versionInReview,
      );
      expect(
        classifyRefusal(
          409,
          '{"errors":[{"detail":"Another build is currently in review for this train."}]}',
        ),
        AppleRefusal.versionInReview,
      );
    });

    test('recognises the six-a-day limit', () {
      expect(
        classifyRefusal(
          409,
          '{"errors":[{"detail":"You have reached the maximum number of builds you can submit for review in a 24 hour period."}]}',
        ),
        AppleRefusal.dailyReviewLimit,
      );
      expect(
        classifyRefusal(
          409,
          '{"errors":[{"detail":"Maximum number of beta app review submissions in a 24-hour period exceeded."}]}',
        ),
        AppleRefusal.dailyReviewLimit,
      );
    });

    test('treats a build already in the group as the job being done', () {
      expect(
        classifyRefusal(
          409,
          '{"errors":[{"detail":"The specified relationship already exists."}]}',
        ),
        AppleRefusal.alreadyAssigned,
      );
    });

    test('does not guess at anything else', () {
      expect(
        classifyRefusal(
          401,
          '{"errors":[{"detail":"Authentication credentials are missing or invalid."}]}',
        ),
        AppleRefusal.other,
      );
      expect(classifyRefusal(409, 'not json at all'), AppleRefusal.other);
      // An expired certificate is not a rate limit, and reporting it as one
      // would send somebody to wait out a day that was never the problem.
      expect(
        classifyRefusal(
          409,
          '{"errors":[{"detail":"The provided entity includes an attribute with a value that is not valid."}]}',
        ),
        AppleRefusal.other,
      );
    });
  });

  group('the review submission', () {
    test('names the build, which is all Apple asks for', () {
      expect(reviewSubmission('b1'), {
        'data': {
          'type': 'betaAppReviewSubmissions',
          'relationships': {
            'build': {
              'data': {'type': 'builds', 'id': 'b1'},
            },
          },
        },
      });
    });

    test('treats a build submitted already as the job being done', () {
      const body =
          '{"errors":[{"status":"409","detail":"A beta app review submission already exists for this build."}]}';
      expect(classifyRefusal(409, body), AppleRefusal.alreadySubmitted);
    });
  });

  group('What to Test', () {
    test('updates the en-US localization when the build has one', () {
      final request = whatsNewRequest(
        {
          'data': [
            {
              'id': 'l-de',
              'attributes': {'locale': 'de-DE'},
            },
            {
              'id': 'l-en',
              'attributes': {'locale': 'en-US'},
            },
          ],
        },
        'b1',
        'notes',
      );
      expect(request.method, 'PATCH');
      expect(request.path, '/v1/betaBuildLocalizations/l-en');
      expect((request.body['data'] as Map)['attributes'], {
        'whatsNew': 'notes',
      });
    });

    test('creates it when the build has none', () {
      final request = whatsNewRequest(const {}, 'b1', 'notes');
      expect(request.method, 'POST');
      expect(request.path, '/v1/betaBuildLocalizations');
      final data = request.body['data'] as Map;
      expect(data['attributes'], {'locale': 'en-US', 'whatsNew': 'notes'});
      expect(data['relationships'], {
        'build': {
          'data': {'type': 'builds', 'id': 'b1'},
        },
      });
    });

    test('drops Markdown emphasis and keeps within 4,000 characters', () {
      expect(whatsNewText('- **Bold** line\n'), '- Bold line');
      expect(whatsNewText('x' * 5000).length, 4000);
    });
  });

  group('redaction', () {
    test('takes a token out of anything on its way to the log', () {
      const token =
          'eyJhbGciOiJFUzI1NiIsImtpZCI6IkFCQ0QxMjM0RUYifQ.eyJpc3MiOiJ4In0.c2ln';
      expect(redact('openssl said: Bearer $token'), isNot(contains('eyJ')));
      expect(redact('openssl said: Bearer $token'), contains('redacted'));
    });

    test('takes the key out too', () {
      const key =
          '-----BEGIN PRIVATE KEY-----\nMIGTAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----';
      final cleaned = redact('could not read $key, sorry');
      expect(cleaned, isNot(contains('MIGTAgEAMBMGByqGSM49')));
      expect(cleaned, contains('sorry'));
    });

    test('leaves ordinary text alone', () {
      expect(
        redact('build 276 is still PROCESSING'),
        'build 276 is still PROCESSING',
      );
    });
  });

  group('the command line', () {
    test('reads both spellings of an option', () {
      expect(parseArgs(['--build=276', '--group', 'External testers']), {
        'build': '276',
        'group': 'External testers',
      });
    });

    test('rejects what it does not understand rather than ignoring it', () {
      expect(() => parseArgs(['276']), throwsFormatException);
      expect(() => parseArgs(['--build']), throwsFormatException);
    });
  });
}

List<int> _hex(String s) => [
  for (var i = 0; i < s.length; i += 2)
    int.parse(s.substring(i, i + 2), radix: 16),
];

String _toHex(List<int> b) =>
    b.map((x) => x.toRadixString(16).padLeft(2, '0')).join();

/// The DER an ECDSA signature normally arrives in, rebuilt from `R||S` so
/// that openssl can be asked to verify one. Only the test needs this
/// direction; the script only ever goes the other way.
List<int> _toDer(List<int> r, List<int> s) {
  List<int> asInteger(List<int> value) {
    var start = 0;
    while (start < value.length - 1 && value[start] == 0) {
      start++;
    }
    final trimmed = value.sublist(start);
    final body = trimmed.first >= 0x80 ? [0, ...trimmed] : trimmed;
    return [0x02, body.length, ...body];
  }

  final contents = [...asInteger(r), ...asInteger(s)];
  return [0x30, contents.length, ...contents];
}

final bool _openssl =
    Process.runSync('sh', ['-c', 'command -v openssl']).exitCode == 0;
