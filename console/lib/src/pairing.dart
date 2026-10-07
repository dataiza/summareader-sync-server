import 'dart:convert';

/// What the QR code carries: the address of this server and a device token,
/// and deliberately nothing else.
///
/// The app's own pairing code carries the library's master key as well, and
/// scanning one of those means "join this library". The server has never held
/// that key — it stores ciphertext and could not read a library if it wanted
/// to — so a code minted here says only "here is my server, here is a token",
/// which is exactly what typing those two things by hand would say. The shape
/// is a contract with the app; a renamed field is a phone that refuses to scan.
///
/// Version 2 spells the fields out. They were `v`, `u` and `t`, which is fine
/// for a QR and useless to the person reading the same code as text, deciding
/// whether it is the one that carries a key. The app reads both spellings, so
/// an older phone scanning this still pairs; a version older than that reads
/// neither and says the code is not one it knows, which is the honest answer.
///
/// The fingerprint since card 713: the SHA-256 of the server's certificate,
/// which the app pins and trusts instead of any authority. Still version 2,
/// because an app that does not know the field ignores it and then refuses
/// the certificate on its own — it fails closed, and sends nothing.
String pairingPayload(String serverUrl, String token, {String? fingerprint}) =>
    jsonEncode({
      'version': 2,
      'server': serverUrl,
      'device_token': token,
      'server_fingerprint': ?fingerprint,
    });

/// The whole difference between the two questions the pairing button can be
/// asked.
///
/// With no devices there is no library and one has to be made from here,
/// because until a device has a token there is nobody to authorise the
/// request. With devices, the library exists — so what this issues is a token
/// into *that* library, never a second one, and the code it draws carries no
/// key because the server has never held one. That makes it the answer for a
/// device coming back to a library it already has, and not the answer for one
/// that has never seen it.
String pairButtonText(int devices) =>
    devices > 0 ? 'Add a device' : 'Create first device';
