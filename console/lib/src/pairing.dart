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
String pairingPayload(String serverUrl, String token) =>
    jsonEncode({'version': 2, 'server': serverUrl, 'device_token': token});

/// The whole difference between the two questions the pairing button can be
/// asked.
///
/// With no devices there is no library and one has to be made from here,
/// because until a device has a token there is nobody to authorise the
/// request. With devices, the library exists and the key that makes it
/// readable lives on those devices — so the honest answer is an explanation,
/// not another library.
String pairButtonText(int devices) =>
    devices > 0 ? 'Add a device' : 'Create first device';
