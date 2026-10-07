import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:summareader_sync_console/src/pairing.dart';
import 'package:summareader_sync_console/src/server.dart';
import 'package:summareader_sync_console/src/service.dart';

// Card 713. The console runs on the server's own machine, reads the server's
// certificate out of its data directory, shows its fingerprint, and pins its
// own connections to it.
//
// No widget tests in this file: one would bring in the test binding, which
// answers every HttpClient with a 400 and never opens a socket, and the point
// here is a real TLS handshake. The window's half is in console_test.dart.

// A throwaway certificate and key for these tests and nothing else. Public on
// purpose: it protects nothing, and generating one per run would put openssl
// on the list of things the suite needs.
const _fixtureCert = '''
-----BEGIN CERTIFICATE-----
MIIBfDCCASGgAwIBAgIUKYGxLm/wQrOX/3kQtAv77iFmN64wCgYIKoZIzj0EAwIw
EjEQMA4GA1UEAwwHZml4dHVyZTAgFw0yNjEwMDcwMTU5MjhaGA8yMTI2MDkxMzAx
NTkyOFowEjEQMA4GA1UEAwwHZml4dHVyZTBZMBMGByqGSM49AgEGCCqGSM49AwEH
A0IABAbqzjQUtGDZ/1lhM6SrCpw7FQg6ja1xujyxpSI5ocnB2iGUs4H4GOR6fF14
tGyITnclyyE1Z0NZLGqOEB1Dw6ejUzBRMB0GA1UdDgQWBBRNkKmx8Z6FLMiJgBM4
Rz1wDocarjAfBgNVHSMEGDAWgBRNkKmx8Z6FLMiJgBM4Rz1wDocarjAPBgNVHRMB
Af8EBTADAQH/MAoGCCqGSM49BAMCA0kAMEYCIQDOZWaiUUc0YBeEHI7/k0bm6wgi
RIVKmjrsJJjbKTMJxwIhANHTPmFJwLMzm+jBdWmsLc4pPZvUKnh3iE9eXZkFh74f
-----END CERTIFICATE-----
''';

const _fixtureKey = '''
-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgNoy2te96KGtSqEbw
7mmPrAmaiZFR3JFL5uBGd5CsZXKhRANCAAQG6s40FLRg2f9ZYTOkqwqcOxUIOo2t
cbo8saUiOaHJwdohlLOB+BjkenxdeLRsiE53JcshNWdDWSxqjhAdQ8On
-----END PRIVATE KEY-----
''';

// What `openssl x509 -outform DER | sha256sum` says of the certificate above.
const _fixtureFingerprint =
    'f32b2c8bd7968e71a17942945566c256da5c3603e57f3ab6f4c288efc57c50ae';

void main() {
  test('the fingerprint is read from the data directory', () {
    final dir = Directory.systemTemp.createTempSync('console-cert');
    addTearDown(() => dir.deleteSync(recursive: true));

    // A server that has never started has no certificate, and the console
    // then speaks plain HTTP to whatever older server is there.
    expect(certificateFingerprint(dir.path), isNull);

    File('${dir.path}/$certFile').writeAsStringSync(_fixtureCert);
    expect(certificateFingerprint(dir.path), _fixtureFingerprint);
  });

  test('it reads in pairs, for a person comparing two screens', () {
    expect(displayFingerprint('ab' * 32), List.filled(32, 'AB').join(':'));
  });

  group('the console talks to its server', () {
    late HttpServer https;
    late Directory dir;
    var requests = 0;

    setUp(() async {
      requests = 0;
      dir = Directory.systemTemp.createTempSync('console-pin');
      final context = SecurityContext()
        ..useCertificateChainBytes(utf8.encode(_fixtureCert))
        ..usePrivateKeyBytes(utf8.encode(_fixtureKey));
      https = await HttpServer.bindSecure('127.0.0.1', 0, context);
      https.listen((request) {
        requests++;
        request.response
          ..headers.contentType = ContentType.json
          ..write(jsonEncode({'devices': <Object>[]}))
          ..close();
      });
    });

    tearDown(() async {
      await https.close(force: true);
      dir.deleteSync(recursive: true);
    });

    SyncServer serverWith(String cert) {
      File('${dir.path}/$certFile').writeAsStringSync(cert);
      return SyncServer(
        exe: null,
        dir: dir.path,
        tokens: const Tokens(metrics: 'm', operator: 'o'),
        addr: '127.0.0.1:${https.port}',
        managedBy: () => false,
      );
    }

    test('over https, pinned to the certificate in its directory', () async {
      final server = serverWith(_fixtureCert);
      expect(server.fingerprint, _fixtureFingerprint);
      expect(await server.overview(), {'devices': <Object>[]});
      expect(requests, 1);
    });

    test('and refuses a server whose certificate is not that one', () async {
      // The directory holds a different certificate from the one served: the
      // console must not send its token to whoever answered.
      final other = _fixtureCert.replaceFirst('MIIBfDCC', 'MIIBfDCD');
      final server = serverWith(other);
      expect(server.fingerprint, isNot(_fixtureFingerprint));
      expect(await server.overview(), isNull);
      expect(await server.rename('d', 'x'), 'could not reach the server');
      expect(requests, 0, reason: 'a request went over an unpinned connection');
    });
  });

  test('the code carries the fingerprint, and the address is https', () {
    final decoded =
        jsonDecode(
              pairingPayload(
                'https://10.10.20.1:8099',
                'a-token',
                fingerprint: _fixtureFingerprint,
              ),
            )
            as Map<String, dynamic>;
    expect(decoded['server'], 'https://10.10.20.1:8099');
    expect(decoded['server_fingerprint'], _fixtureFingerprint);
    // Still version 2: an app that does not know the field fails closed on
    // the certificate by itself.
    expect(decoded['version'], 2);
  });
}
