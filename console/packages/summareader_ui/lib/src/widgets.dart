import 'package:flutter/material.dart';

import 'theme.dart';

/// A widget that changes appearance on hover, which is how the prototype gives
/// feedback — it has no ripples.
class Hoverable extends StatefulWidget {
  const Hoverable({
    super.key,
    required this.builder,
    this.onTap,
    this.cursor,
    this.label,
  });

  final Widget Function(BuildContext context, bool hovered) builder;
  final VoidCallback? onTap;
  final MouseCursor? cursor;

  /// What this control is called, for anything that cannot see it.
  ///
  /// Required in practice wherever the child is an icon and nothing else: an
  /// icon has no text for a screen reader to read, so an unlabelled one is
  /// announced as "button" and nothing more. Text buttons do not need it —
  /// their own text is already the label.
  final String? label;

  @override
  State<Hoverable> createState() => _HoverableState();
}

class _HoverableState extends State<Hoverable> {
  bool _hovered = false;

  /// Big enough to hit with a thumb, on the windows where a thumb is what is
  /// being used.
  ///
  /// A pointer is precise and a fingertip is about nine millimetres across,
  /// so 48 logical pixels is the floor on a phone and pointless bloat on a
  /// desktop, where these same controls sit at 26 to 38 and are perfectly
  /// usable with a mouse. Decided by window width rather than by platform: a
  /// small window on a touchscreen laptop has the same problem, and a
  /// phone-sized desktop window is not worth a second rule.
  Widget _sized(BuildContext context, Widget child) {
    if (widget.onTap == null) return child;
    final width = MediaQuery.maybeSizeOf(context)?.width;
    if (width == null || width >= narrowWindow) return child;

    return ConstrainedBox(
      constraints: const BoxConstraints(minWidth: 48, minHeight: 48),
      // The control keeps its own size and gains room around it, rather than
      // being stretched into a shape it was never drawn for.
      child: Center(widthFactor: 1, heightFactor: 1, child: child),
    );
  }

  @override
  Widget build(BuildContext context) {
    return MouseRegion(
      cursor:
          widget.cursor ??
          (widget.onTap == null ? MouseCursor.defer : SystemMouseCursors.click),
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      // Only where a label was given. Wrapping every one of these added a
      // node carrying the tap and no text, with the text in a child — which
      // is precisely the shape "unlabelled button" means, so it broke the
      // controls whose own text was already doing the job.
      child: switch (widget.label) {
        final label? => Semantics(
          label: label,
          button: widget.onTap != null,
          child: GestureDetector(
            onTap: widget.onTap,
            behavior: HitTestBehavior.opaque,
            child: _sized(context, widget.builder(context, _hovered)),
          ),
        ),
        null => GestureDetector(
          onTap: widget.onTap,
          behavior: HitTestBehavior.opaque,
          child: _sized(context, widget.builder(context, _hovered)),
        ),
      },
    );
  }
}

class PillButton extends StatelessWidget {
  const PillButton({
    super.key,
    required this.label,
    this.icon,
    this.onTap,
    this.height = 36,
    this.selected = false,
    this.tooltip,
  });

  /// Null to show the icon alone, where a window is too narrow for words.
  /// Something with no label and no icon would be a blank button, which is
  /// asserted against rather than rendered.
  final String? label;

  /// What it is called when the label is not shown. Kept separate so an
  /// icon-only button still has a name for a screen reader and a tooltip for
  /// a mouse.
  final String? tooltip;
  final IconData? icon;
  final VoidCallback? onTap;
  final double height;
  final bool selected;

  @override
  Widget build(BuildContext context) {
    assert(label != null || icon != null, 'a button with neither is a blank');
    final named = label ?? tooltip;
    final button = Hoverable(
      label: label == null ? named : null,
      onTap: onTap,
      builder: (context, hovered) => Container(
        height: height,
        padding: EdgeInsets.symmetric(horizontal: label == null ? 10 : 14),
        decoration: BoxDecoration(
          color: selected
              ? Ar.accent200
              : hovered
              ? Ar.dim(0.07)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(Ar.pill),
          border: Border.all(color: Ar.divider),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[
              Icon(icon, size: 15, color: Ar.text),
              if (label != null) const SizedBox(width: 7),
            ],
            // Truncates rather than overflowing. These sit in a Wrap that is
            // only as wide as its column, and beside an open item that column
            // is narrower than any of these labels were designed for.
            if (label case final text?)
              Flexible(
                child: Text(
                  text,
                  overflow: TextOverflow.ellipsis,
                  style: Ar.bodyStyle(13 * chromeScale(context)),
                ),
              ),
          ],
        ),
      ),
    );

    return tooltip == null || label != null
        ? button
        : Tooltip(message: tooltip!, child: button);
  }
}

/// The filled call-to-action.
class PrimaryButton extends StatelessWidget {
  const PrimaryButton({
    super.key,
    required this.label,
    this.icon,
    this.onTap,
    this.height = 40,
    this.expand = false,
  });

  final String label;
  final IconData? icon;
  final VoidCallback? onTap;
  final double height;
  final bool expand;

  @override
  Widget build(BuildContext context) {
    return Hoverable(
      onTap: onTap,
      builder: (context, hovered) => Container(
        height: height,
        width: expand ? double.infinity : null,
        padding: const EdgeInsets.symmetric(horizontal: 18),
        // Only when it is meant to be wide. A Container with an alignment
        // takes all the width it is offered, which is invisible inside a Row
        // — children there are measured unbounded — and turns the button into
        // a full-width bar the moment it is put in a Wrap or a Column.
        alignment: expand ? Alignment.center : null,
        decoration: BoxDecoration(
          color: hovered ? Ar.accent600 : Ar.accent,
          borderRadius: BorderRadius.circular(Ar.pill),
        ),
        child: Row(
          mainAxisSize: expand ? MainAxisSize.max : MainAxisSize.min,
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            if (icon != null) ...[
              Icon(icon, size: 16, color: Ar.bg),
              const SizedBox(width: 7),
            ],
            Flexible(
              child: Text(
                label,
                overflow: TextOverflow.ellipsis,
                style: Ar.bodyStyle(14, weight: FontWeight.w600, color: Ar.bg),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The soft accent-tinted action, used for "Summarize" and highlight controls.
class SoftButton extends StatelessWidget {
  const SoftButton({
    super.key,
    required this.label,
    this.icon,
    this.onTap,
    this.height = 30,
  });

  final String label;
  final IconData? icon;
  final VoidCallback? onTap;
  final double height;

  @override
  Widget build(BuildContext context) {
    return Hoverable(
      onTap: onTap,
      builder: (context, hovered) => Container(
        height: height,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        decoration: BoxDecoration(
          color: hovered ? Ar.accent200 : Ar.accent100,
          borderRadius: BorderRadius.circular(Ar.pill),
          border: Border.all(color: Ar.accent300),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[
              Icon(icon, size: 14, color: Ar.accent800),
              const SizedBox(width: 6),
            ],
            Flexible(
              child: Text(
                label,
                overflow: TextOverflow.ellipsis,
                style: Ar.bodyStyle(12.5, color: Ar.accent800),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A selectable segment, used for text size, column width, length and scope.
class Segment extends StatelessWidget {
  const Segment({
    super.key,
    required this.label,
    required this.selected,
    this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Hoverable(
      // Its own text, said out loud. A segment is a labelled control by
      // nature, but the label lives in a Text the tap node does not carry.
      label: label,
      onTap: onTap,
      builder: (context, hovered) => Container(
        height: 32,
        padding: const EdgeInsets.symmetric(horizontal: 13),
        decoration: BoxDecoration(
          color: selected
              ? Ar.accent
              : hovered
              ? Ar.dim(0.07)
              : Ar.neutral100,
          borderRadius: BorderRadius.circular(Ar.pill),
          border: Border.all(color: selected ? Ar.accent : Ar.divider),
        ),
        // `Center` with a width factor rather than the Container's own
        // `alignment`: a Container with alignment set and no width expands to
        // fill its constraints instead of hugging the label. Inside a Row that
        // is invisible, but in a Wrap it makes every chip full width.
        child: Center(
          widthFactor: 1,
          child: Text(
            label,
            style: Ar.bodyStyle(
              12.5,
              weight: selected ? FontWeight.w600 : FontWeight.w400,
              color: selected ? Ar.bg : Ar.text,
            ),
          ),
        ),
      ),
    );
  }
}

/// The tinted count chip. Topics use the second accent; filters use it too.
class Tag extends StatelessWidget {
  const Tag({
    super.key,
    required this.label,
    this.background,
    this.foreground,
    this.fontSize = 11.5,
  });

  final String label;
  final Color? background;
  final Color? foreground;
  final double fontSize;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
      decoration: BoxDecoration(
        color: background ?? Ar.accent2100,
        borderRadius: BorderRadius.circular(Ar.pill),
      ),
      child: Text(
        label,
        style: Ar.bodyStyle(fontSize, color: foreground ?? Ar.accent2800),
      ),
    );
  }
}

/// The two-state switch used per source and in settings.
class ArSwitch extends StatelessWidget {
  const ArSwitch({super.key, required this.value, this.onChanged, this.label});

  final bool value;
  final ValueChanged<bool>? onChanged;

  /// What this switch governs.
  ///
  /// A switch is nothing but a coloured shape: it has no text of its own, so
  /// without this a screen reader announces a toggle and never says what it
  /// toggles. The row beside it reads the name — but that is a separate
  /// widget, and the two are only related by sitting next to each other.
  final String? label;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      // Announced as a switch that is on or off, rather than as a button
      // whose effect has to be inferred from what happens after pressing it.
      toggled: value,
      enabled: onChanged != null,
      child: Hoverable(
        label: label,
        onTap: onChanged == null ? null : () => onChanged!(!value),
        builder: (context, hovered) => AnimatedContainer(
          duration: const Duration(milliseconds: 140),
          width: 38,
          height: 22,
          padding: const EdgeInsets.all(2),
          decoration: BoxDecoration(
            color: value ? Ar.accent2500 : Ar.neutral300,
            borderRadius: BorderRadius.circular(Ar.pill),
          ),
          child: AnimatedAlign(
            duration: const Duration(milliseconds: 140),
            alignment: value ? Alignment.centerRight : Alignment.centerLeft,
            child: Container(
              width: 18,
              height: 18,
              decoration: BoxDecoration(
                color: Ar.neutral100,
                shape: BoxShape.circle,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A radio row: the label, the hint underneath, and a filled dot when picked.
class RadioRow extends StatelessWidget {
  const RadioRow({
    super.key,
    required this.label,
    this.hint,
    required this.selected,
    this.onTap,
  });

  final String label;

  /// The line under the label. Optional: some choices are their own
  /// explanation, and an empty line under each of them is worse than none.
  final String? hint;
  final bool selected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return Hoverable(
      onTap: onTap,
      builder: (context, hovered) => Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: selected
              ? Ar.accent100
              : hovered
              ? Ar.dim(0.05)
              : Ar.surface,
          borderRadius: BorderRadius.circular(Ar.radiusMd),
          border: Border.all(
            color: selected ? Ar.accent300 : Colors.transparent,
          ),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 16,
              height: 16,
              margin: const EdgeInsets.only(top: 2, right: 12),
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(
                  color: selected ? Ar.accent : Ar.neutral400,
                  width: 2,
                ),
                color: selected ? Ar.accent : Colors.transparent,
              ),
              child: selected
                  ? Center(
                      child: Container(
                        width: 5,
                        height: 5,
                        decoration: BoxDecoration(
                          color: Ar.bg,
                          shape: BoxShape.circle,
                        ),
                      ),
                    )
                  : null,
            ),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(label, style: Ar.bodyStyle(14, weight: FontWeight.w600)),
                  if (hint != null) ...[
                    const SizedBox(height: 2),
                    Text(
                      hint!,
                      style: Ar.bodyStyle(
                        12.5,
                        color: Ar.dim(0.7),
                        height: 1.5,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A rounded icon badge, sized to the group headers and item thumbnails.
class KindBadge extends StatelessWidget {
  const KindBadge({
    super.key,
    required this.icon,
    this.size = 30,
    this.background,
    this.foreground,
  });

  final IconData icon;
  final double size;
  final Color? background;
  final Color? foreground;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: background ?? Ar.accent2200,
        shape: BoxShape.circle,
      ),
      child: Icon(icon, size: size * 0.5, color: foreground ?? Ar.accent2800),
    );
  }
}

/// The design's section eyebrow — uppercase, tracked out, accent-coloured.
class Eyebrow extends StatelessWidget {
  const Eyebrow(this.text, {super.key});

  final String text;

  @override
  Widget build(BuildContext context) =>
      Text(text.toUpperCase(), style: Ar.eyebrow);
}

/// A rounded search/text field on the surface colour.
class ArField extends StatelessWidget {
  const ArField({
    super.key,
    required this.controller,
    this.hint,
    this.icon,
    this.height = 38,
    this.fontSize = 14,
    this.background,
    this.onChanged,
    this.onSubmitted,
    this.autofocus = false,
    this.focusNode,
  });

  final TextEditingController controller;
  final String? hint;
  final IconData? icon;
  final double height;
  final double fontSize;
  final Color? background;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;
  final bool autofocus;

  /// For a field that has to be *given* focus rather than merely ask for it.
  ///
  /// `autofocus` only applies when nothing in the enclosing scope has focus,
  /// and the shell holds focus for the keyboard shortcuts — so a field that
  /// wants the caret has to take it.
  final FocusNode? focusNode;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: height,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      decoration: BoxDecoration(
        color: background ?? Ar.surface,
        borderRadius: BorderRadius.circular(Ar.pill),
      ),
      child: Row(
        children: [
          if (icon != null) ...[
            Icon(icon, size: 16, color: Ar.dim(0.55)),
            const SizedBox(width: 8),
          ],
          Expanded(
            child: TextField(
              controller: controller,
              onChanged: onChanged,
              onSubmitted: onSubmitted,
              autofocus: autofocus,
              focusNode: focusNode,
              style: Ar.bodyStyle(fontSize),
              cursorColor: Ar.accent,
              decoration: InputDecoration(
                isDense: true,
                border: InputBorder.none,
                hintText: hint,
                hintStyle: Ar.bodyStyle(fontSize, color: Ar.dim(0.45)),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The dark pill that slides up from the bottom to confirm an action.
class ArToast extends StatelessWidget {
  const ArToast(this.message, {super.key, this.actionLabel, this.onAction});

  final String message;

  /// A second reading of what just happened, offered rather than asked.
  ///
  /// A pasted address is classified without a dialog, so this is where the
  /// other answer lives: "Added as a source · Save as link instead". Null on
  /// every toast that is only telling you something.
  final String? actionLabel;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    final action = actionLabel;
    return Align(
      alignment: Alignment.bottomCenter,
      child: Padding(
        padding: const EdgeInsets.only(bottom: 26),
        child: Container(
          padding: EdgeInsets.only(
            left: 20,
            // Less on the right when a button follows: the label carries its
            // own padding, and 20 after it reads as a gap rather than an edge.
            right: action == null ? 20 : 8,
            top: 12,
            bottom: 12,
          ),
          decoration: BoxDecoration(
            color: Ar.neutral900,
            borderRadius: BorderRadius.circular(Ar.pill),
            boxShadow: Ar.shadowLg,
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Flexible(
                child: Text(
                  message,
                  style: Ar.bodyStyle(13.5, color: Ar.neutral100),
                ),
              ),
              if (action != null) ...[
                const SizedBox(width: 14),
                Hoverable(
                  onTap: onAction,
                  label: action,
                  builder: (context, hovered) => Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 6,
                    ),
                    decoration: BoxDecoration(
                      color: hovered
                          ? Ar.neutral100.withValues(alpha: 0.18)
                          : Ar.neutral100.withValues(alpha: 0.10),
                      borderRadius: BorderRadius.circular(Ar.pill),
                    ),
                    child: Text(
                      action,
                      style: Ar.bodyStyle(13, color: Ar.neutral100),
                    ),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// A card on the surface colour that lifts on hover.
class SurfaceCard extends StatelessWidget {
  const SurfaceCard({
    super.key,
    required this.child,
    this.onTap,
    this.padding = const EdgeInsets.all(15),
    this.background,
    this.radius = Ar.radiusMd,
  });

  final Widget child;
  final VoidCallback? onTap;
  final EdgeInsets padding;
  final Color? background;
  final double radius;

  @override
  Widget build(BuildContext context) {
    return Hoverable(
      onTap: onTap,
      builder: (context, hovered) => AnimatedContainer(
        duration: const Duration(milliseconds: 120),
        padding: padding,
        decoration: BoxDecoration(
          color: background ?? Ar.surface,
          borderRadius: BorderRadius.circular(radius),
          boxShadow: hovered && onTap != null ? Ar.shadowMd : null,
        ),
        child: child,
      ),
    );
  }
}
