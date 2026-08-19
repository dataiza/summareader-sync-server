import 'package:flutter/material.dart';

/// Where a window stops being a desktop and starts being a phone.
///
/// Here rather than in the app because the widgets that change shape at this
/// width live here, and a breakpoint defined somewhere else is one that
/// disagrees with them eventually.
const narrowWindow = 600.0;

/// One set of colours. Two exist: the paper one the design bundle specifies,
/// and a dark one for reading at night.
///
/// The dark palette is not a second design — it is the same hues with each
/// ramp reversed and the ground and ink swapped. `neutral100` is a near-white
/// surface in daylight and a near-black one at night, `accent700` is eyebrow
/// text that becomes `accent300`, and every widget in the app keeps the token
/// it already asks for.
class ArPalette {
  const ArPalette({
    required this.bg,
    required this.surface,
    required this.text,
    required this.accent,
    required this.accent2,
    required this.neutrals,
    required this.accents,
    required this.accent2s,
    required this.brightness,
  });

  final Color bg;
  final Color surface;
  final Color text;
  final Color accent;
  final Color accent2;

  /// Nine steps, 100 to 900, lightest first in daylight and darkest first at
  /// night.
  final List<Color> neutrals;
  final List<Color> accents;
  final List<Color> accent2s;

  final Brightness brightness;

  /// The same ramp, read from the other end, with the ground and the ink
  /// exchanged for ones that are easy on a dark room.
  ArPalette get inverted => ArPalette(
        // Not black: a warm near-black keeps the paper feeling and is what
        // long reading in a dark room is comfortable against.
        bg: const Color(0xFF1A1613),
        surface: const Color(0xFF241F1A),
        text: const Color(0xFFF0E7D8),
        accent: const Color(0xFFE08C51),
        accent2: const Color(0xFF9DAE7C),
        neutrals: neutrals.reversed.toList(),
        accents: accents.reversed.toList(),
        accent2s: accent2s.reversed.toList(),
        brightness: Brightness.dark,
      );
}

/// The design system's tokens, transcribed from the handoff's `styles.css`.
///
/// These are values, not decisions — every one of them comes from the design
/// bundle. Where the CSS uses `color-mix(in srgb, X n%, transparent)`, the
/// equivalent here is [Color.withValues] on the same base, which is the same
/// operation against an opaque ground.
/// How much smaller the interface's own words are on a small screen.
///
/// Not the article: that is the Reading setting, and a reader who chose a
/// measure did not ask for it to shrink because the window did. This is the
/// chrome — headings, buttons, the labels on a card — which was drawn for a
/// desktop and, at desktop sizes on a phone, leaves the reading matter
/// competing with its own furniture.
double chromeScale(BuildContext context) =>
    MediaQuery.sizeOf(context).width < 480 ? 0.78 : 1.0;

class Ar {
  const Ar._();

  /// The palette every token below reads from.
  ///
  /// A module-level value rather than something looked up from a context: the
  /// tokens are used in `const`-adjacent places and in a few helpers that have
  /// no BuildContext, and threading one through 500 call sites to change a
  /// colour would be a refactor pretending to be a feature. [use] is called
  /// before the first frame and whenever the choice changes.
  static ArPalette _palette = daylight;

  static ArPalette get palette => _palette;

  static void use(Brightness brightness) =>
      _palette = brightness == Brightness.dark ? daylight.inverted : daylight;

  /// The design bundle's own colours, unchanged.
  static const daylight = ArPalette(
    bg: Color(0xFFF5EAD8),
    surface: Color(0xFFEBDDC5),
    text: Color(0xFF201E1D),
    accent: Color(0xFFC67139),
    accent2: Color(0xFF7A8A5E),
    neutrals: [
      Color(0xFFF9F4ED),
      Color(0xFFEEE7DB),
      Color(0xFFDCD3C4),
      Color(0xFFC0B6A5),
      Color(0xFFA19786),
      Color(0xFF82796A),
      Color(0xFF645C50),
      Color(0xFF474238),
      Color(0xFF2E2B25),
    ],
    accents: [
      Color(0xFFFFF2EB),
      Color(0xFFFFE1D0),
      Color(0xFFFFC6A5),
      Color(0xFFF6A06B),
      Color(0xFFD67F48),
      Color(0xFFB2622D),
      Color(0xFF8C491A),
      Color(0xFF643312),
      Color(0xFF402310),
    ],
    accent2s: [
      Color(0xFFF0FAE1),
      Color(0xFFE1EECC),
      Color(0xFFCCDBB2),
      Color(0xFFAEBF92),
      Color(0xFF8FA073),
      Color(0xFF728157),
      Color(0xFF56633F),
      Color(0xFF3D472B),
      Color(0xFF272E1B),
    ],
    brightness: Brightness.light,
  );

  // Role colors.
  static Color get bg => _palette.bg;
  static Color get surface => _palette.surface;
  static Color get text => _palette.text;
  static Color get accent => _palette.accent;
  static Color get accent2 => _palette.accent2;

  /// `color-mix(in srgb, text 16%, transparent)`.
  static Color get divider => text.withValues(alpha: 0.16);

  // Neutral ramp.
  static Color get neutral100 => _palette.neutrals[0];
  static Color get neutral200 => _palette.neutrals[1];
  static Color get neutral300 => _palette.neutrals[2];
  static Color get neutral400 => _palette.neutrals[3];
  static Color get neutral500 => _palette.neutrals[4];
  static Color get neutral600 => _palette.neutrals[5];
  static Color get neutral700 => _palette.neutrals[6];
  static Color get neutral800 => _palette.neutrals[7];
  static Color get neutral900 => _palette.neutrals[8];

  // Accent ramp.
  static Color get accent100 => _palette.accents[0];
  static Color get accent200 => _palette.accents[1];
  static Color get accent300 => _palette.accents[2];
  static Color get accent400 => _palette.accents[3];
  static Color get accent500 => _palette.accents[4];
  static Color get accent600 => _palette.accents[5];
  static Color get accent700 => _palette.accents[6];
  static Color get accent800 => _palette.accents[7];
  static Color get accent900 => _palette.accents[8];

  // Second accent ramp.
  static Color get accent2100 => _palette.accent2s[0];
  static Color get accent2200 => _palette.accent2s[1];
  static Color get accent2300 => _palette.accent2s[2];
  static Color get accent2400 => _palette.accent2s[3];
  static Color get accent2500 => _palette.accent2s[4];
  static Color get accent2600 => _palette.accent2s[5];
  static Color get accent2700 => _palette.accent2s[6];
  static Color get accent2800 => _palette.accent2s[7];
  static Color get accent2900 => _palette.accent2s[8];

  /// A 1.10× spacing scale.
  static const space1 = 4.4;
  static const space2 = 8.8;
  static const space3 = 13.2;
  static const space4 = 17.6;
  static const space6 = 26.4;
  static const space8 = 35.2;

  static const radiusSm = 8.0;
  static const radiusMd = 16.0;
  static const radiusLg = 28.0;

  /// Fully rounded — the design uses `999px` throughout for pills.
  static const pill = 999.0;

  static const heading = 'Caprasimo';
  static const body = 'Figtree';

  /// The three elevations, tuned against the light ground.
  static List<BoxShadow> get shadowSm => [
        BoxShadow(
          color: neutral900.withValues(alpha: 0.14),
          offset: const Offset(0, 1),
          blurRadius: 2,
        ),
      ];

  static List<BoxShadow> get shadowMd => [
        BoxShadow(
          color: neutral900.withValues(alpha: 0.16),
          offset: const Offset(0, 3),
          blurRadius: 10,
        ),
      ];

  static List<BoxShadow> get shadowLg => [
        BoxShadow(
          color: neutral900.withValues(alpha: 0.22),
          offset: const Offset(0, 12),
          blurRadius: 32,
        ),
      ];

  /// Text at a fraction of full opacity, which is how the design expresses
  /// secondary and tertiary copy rather than with separate grey tokens.
  static Color dim(double percent) => text.withValues(alpha: percent);

  /// Whether the display face can draw this text at all.
  ///
  /// Caprasimo is a display face with 246 glyphs: ASCII, Latin-1 and a handful
  /// of punctuation. It has no `ě č ř ž ů ň ď ť` — nothing from Latin
  /// Extended-A beyond `ı Œœ Šš Ÿ` — so a Czech, Slovak, Polish, Hungarian,
  /// Turkish or Baltic headline is drawn in it up to the first diacritic and
  /// in whatever the platform substitutes after that. Rendered, "Vědci
  /// objevili" is four letters of one face, one of another, then back.
  ///
  /// Read out of the font's own cmap rather than guessed. Replacing the
  /// display face means checking this again.
  static bool displayCanRender(String text) {
    for (final rune in text.runes) {
      if (rune >= 0x20 && rune <= 0x7E) continue;
      // Latin-1, less the one hole in it: no MIDDLE DOT.
      if (rune >= 0xA0 && rune <= 0xFF && rune != 0xB7) continue;
      if (_displayExtras.contains(rune)) continue;
      return false;
    }
    return true;
  }

  /// The few things above Latin-1 it does have — the punctuation a headline
  /// actually uses. Its maths symbols are left out: one in a heading is not a
  /// reason to keep the display face.
  static const _displayExtras = <int>{
    0x0131, 0x0152, 0x0153, 0x0160, 0x0161, 0x0178, 0x0192, // ı Œœ Šš Ÿ ƒ
    0x2013, 0x2014, // – —
    0x2018, 0x2019, 0x201A, 0x201C, 0x201D, 0x201E, // ‘’‚ “”„
    0x2020, 0x2021, 0x2022, 0x2026, 0x2030, // †‡• … ‰
    0x2039, 0x203A, 0x20AC, 0x2122, // ‹› € ™
  };

  /// A heading, in the display face where it can do the job.
  ///
  /// Pass [forText] wherever the words are known — which is everywhere they
  /// are content or a translation. A heading the display face cannot draw is
  /// set in the text face at a heavy weight instead: one face for the whole
  /// line reads as a decision, and half a line in each reads as a bug, which
  /// is what it was.
  static TextStyle headingStyle(double size, {String? forText}) {
    final display = forText == null || displayCanRender(forText);
    return TextStyle(
      fontFamily: display ? heading : body,
      // The text face has to work harder to read as a heading: the display
      // one is heavy by design and Figtree at 400 would read as body copy.
      fontWeight: display ? FontWeight.w400 : FontWeight.w800,
      letterSpacing: display ? null : size * -0.02,
      fontSize: size,
      height: 1.1,
      color: text,
      // For the call sites that cannot say what the words are. Mixed faces
      // still, but the app's own text face rather than whatever the platform
      // reaches for.
      fontFamilyFallback: const [body],
    );
  }

  static TextStyle bodyStyle(
    double size, {
    FontWeight weight = FontWeight.w400,
    Color? color,
    double? height,
  }) =>
      TextStyle(
        fontFamily: body,
        fontSize: size,
        fontWeight: weight,
        height: height,
        color: color ?? text,
      );

  /// The uppercase section label used above every group.
  static TextStyle get eyebrow => TextStyle(
        fontFamily: body,
        fontSize: 11,
        letterSpacing: 0.09 * 11,
        color: accent700,
      );

  static ThemeData themeData([Brightness brightness = Brightness.light]) {
    // The palette is set here as well as by the app, so a widget built in a
    // test that only asks for a theme still draws in the matching colours.
    use(brightness);
    return ThemeData(
      useMaterial3: true,
      fontFamily: body,
      scaffoldBackgroundColor: bg,
      colorScheme: ColorScheme.fromSeed(
        seedColor: accent,
        surface: bg,
        primary: accent,
        secondary: accent2,
        brightness: brightness,
      ),
      textTheme: const TextTheme().apply(
        bodyColor: text,
        displayColor: text,
        fontFamily: body,
      ),
      splashFactory: NoSplash.splashFactory,
      // The prototype has no ripples; feedback is colour change on hover.
      highlightColor: Colors.transparent,
    );
  }
}
