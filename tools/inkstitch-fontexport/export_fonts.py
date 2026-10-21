# SPDX-License-Identifier: GPL-3.0-or-later
#
# Export Ink/Stitch lettering fonts to stitch data for TSG Embroidery Pro.
#
# This script drives Ink/Stitch (GPL-3.0) as a library, so it is licensed
# GPL-3.0-or-later. It is a build-time tool only; the app itself never runs or
# links Ink/Stitch, it only reads the stitch data this script writes.
#
# Usage (from an Ink/Stitch v3.3 source checkout with its Python deps):
#   PYTHONPATH=/path/to/inkstitch python export_fonts.py \
#       --fonts-dir /path/to/embroidery-fonts/src --out out_dir \
#       [--only font_dir,font_dir] blank.svg
#
# Output: one JSON per font: metrics, kerning and per-glyph stitches in mm,
# with every glyph rendered alone at the Ink/Stitch line origin (baseline y=0
# is NOT guaranteed; consumers align using the reported metrics).

import json
import os
import signal
import sys
import time
import traceback
import unicodedata


# Ink/Stitch imports wxPython for its GUI dialogs; we never show any, so stub
# out wx (and any wx.* submodule) before importing Ink/Stitch.
import importlib.abc
import importlib.machinery
import types
from unittest import mock


class _WxStubFinder(importlib.abc.MetaPathFinder, importlib.abc.Loader):
    def find_spec(self, name, path, target=None):
        if name == "wx" or name.startswith("wx."):
            return importlib.machinery.ModuleSpec(name, self, is_package=True)
        return None

    def create_module(self, spec):
        m = types.ModuleType(spec.name)
        m.__path__ = []
        cache = {}

        def getattr_(attr):
            if attr.startswith("__"):
                raise AttributeError(attr)
            if attr not in cache:
                # Capitalised names are usually classes that Ink/Stitch subclasses.
                if attr[:1].isupper() and not attr.isupper():
                    cache[attr] = type(attr, (), {"__init__": lambda self, *a, **k: None,
                                                  "__getattr__": lambda self, n: mock.MagicMock()})
                else:
                    cache[attr] = mock.MagicMock(name=f"{spec.name}.{attr}")
            return cache[attr]
        m.__getattr__ = getattr_
        return m

    def exec_module(self, module):
        pass


sys.meta_path.insert(0, _WxStubFinder())

from inkex import Group  # noqa: E402

from lib.extensions.base import InkstitchExtension  # noqa: E402
from lib.lettering.font import Font  # noqa: E402
from lib.stitch_plan import stitch_groups_to_stitch_plan  # noqa: E402
from lib.svg import get_correction_transform  # noqa: E402
from lib.utils import DotDict  # noqa: F401  (kept for parity with batch lettering)

PX_TO_MM = 25.4 / 96

# Licences we will not redistribute (non-commercial / no-derivatives).
EXCLUDE_LICENCES = ("NC", "ND")


GLYPH_SECONDS = 25    # give up on a single glyph after this long
FONT_SECONDS = 420    # give up on a whole font after this long
SKIP_KEYWORDS = ("crossstitch",)  # cross-stitch fonts are very slow and not needed


class GlyphTimeout(Exception):
    pass


def _alarm(signum, frame):
    raise GlyphTimeout()


def keep_glyph(ch, latin_font):
    if len(ch) != 1:
        return False  # ligatures / multi-char glyph names: not used by the app
    cp = ord(ch)
    if cp < 0x20 or unicodedata.category(ch).startswith("C"):
        return False
    if latin_font:
        # Latin, Latin-1, Latin Extended-A/B, general punctuation, euro
        return cp < 0x250 or 0x2010 <= cp <= 0x2044 or cp == 0x20AC
    return True


class FontExport(InkstitchExtension):
    def __init__(self, *args, **kwargs):
        InkstitchExtension.__init__(self)
        self.arg_parser.add_argument("--fonts-dir", dest="fonts_dir")
        self.arg_parser.add_argument("--out", dest="out")
        self.arg_parser.add_argument("--only", dest="only", default="")

    def render(self, font, text):
        lettering_group = Group()
        self.svg.append(lettering_group)
        lettering_group.set("transform", get_correction_transform(lettering_group, child=True))
        dest = Group()
        lettering_group.append(dest)
        font.render_text(text, dest, back_and_forth=False, trim_option=0, color_sort=0)
        self.get_elements(troubleshoot=True)
        groups = self.elements_to_stitch_groups(self.elements) if self.elements else []
        meta = self.get_inkstitch_metadata()
        plan = stitch_groups_to_stitch_plan(groups, collapse_len=meta["collapse_len_mm"], min_stitch_len=meta["min_stitch_len_mm"]) if groups else None
        lettering_group.delete()
        blocks = []
        if plan is None:
            return blocks
        for cb in plan:
            sts = []
            for s in cb:
                flag = 0
                if s.jump:
                    flag = 1
                if s.trim:
                    flag = 2
                if s.stop:
                    flag = 4
                if s.color_change:
                    continue
                sts.append([round(s.x * PX_TO_MM, 3), round(s.y * PX_TO_MM, 3), flag])
            if sts:
                color = cb.color.to_hex_str() if cb.color is not None else "#000000"
                blocks.append({"color": color, "st": sts})
        return blocks

    def effect(self):
        os.makedirs(self.options.out, exist_ok=True)
        only = [x for x in self.options.only.split(",") if x]
        dirs = sorted(os.listdir(self.options.fonts_dir))
        for d in dirs:
            if only and d not in only:
                continue
            path = os.path.join(self.options.fonts_dir, d)
            target = os.path.join(self.options.out, d + ".json")
            if os.path.exists(target):
                continue
            try:
                with open(os.path.join(path, "font.json"), encoding="utf-8") as fh:
                    info = json.load(fh)
            except Exception:
                continue
            if any(k in [str(x).lower() for x in info.get("keywords", [])] for k in SKIP_KEYWORDS):
                print(f"skip {d}: cross-stitch font", file=sys.stderr)
                continue
            lic = str(info.get("font_license", ""))
            if any(x in lic.upper().replace("-", " ").split() or ("-" + x) in lic.upper() for x in EXCLUDE_LICENCES):
                print(f"skip {d}: licence {lic}", file=sys.stderr)
                continue
            t0 = time.time()
            try:
                font = Font(path, show_font_path_warning=False)
                font._load_variants()
                variant = font.get_variant(font.default_variant)
                glyphs = {}
                names = sorted(variant.glyphs.keys()) if hasattr(variant, "glyphs") else []
                latin = sum(1 for n in names if len(n) == 1 and ("A" <= n <= "Z" or "a" <= n <= "z")) >= 20
                signal.signal(signal.SIGALRM, _alarm)
                timed_out = False
                for name in names:
                    if not keep_glyph(name, latin):
                        continue
                    if time.time() - t0 > FONT_SECONDS:
                        timed_out = True
                        break
                    g = variant[name]
                    if g is None:
                        continue
                    signal.alarm(GLYPH_SECONDS)
                    try:
                        blocks = self.render(font, name)
                    except GlyphTimeout:
                        print(f"  {d} {name!r}: timeout", file=sys.stderr)
                        continue
                    except Exception as e:  # one broken glyph must not kill the font
                        print(f"  {d} {name!r}: {e}", file=sys.stderr)
                        continue
                    finally:
                        signal.alarm(0)
                    if not blocks:
                        continue
                    adv = font.horiz_adv_x.get(name, font.horiz_adv_x_default)
                    if adv is None:
                        adv = g.width + g.min_x
                    glyphs[name] = {"minx": g.min_x * PX_TO_MM, "adv": adv * PX_TO_MM, "blocks": blocks}
                if timed_out:
                    print(f"skip {d}: over {FONT_SECONDS}s", file=sys.stderr)
                    continue
                if not glyphs:
                    print(f"skip {d}: no glyphs", file=sys.stderr)
                    continue
                licence_text = ""
                lp = os.path.join(path, "LICENSE")
                if os.path.exists(lp):
                    with open(lp, encoding="utf-8", errors="replace") as fh:
                        licence_text = fh.read()
                out = {
                    "id": d,
                    "name": info.get("name", d),
                    "description": info.get("description", ""),
                    "keywords": info.get("keywords", []),
                    "license": lic,
                    "license_text": licence_text,
                    "original_font": info.get("original_font", ""),
                    "original_font_url": info.get("original_font_url", ""),
                    "size_mm": info.get("size"),
                    "min_scale": info.get("min_scale", 1.0),
                    "max_scale": info.get("max_scale", 1.0),
                    "letter_case": info.get("letter_case", ""),
                    "word_spacing": font.word_spacing * PX_TO_MM,
                    "leading": (font.leading or 0) * PX_TO_MM,
                    "kerning": {k: v * PX_TO_MM for k, v in (font.kerning_pairs or {}).items()},
                    "glyphs": glyphs,
                }
                with open(target + ".tmp", "w", encoding="utf-8") as f:
                    json.dump(out, f, ensure_ascii=False, separators=(",", ":"))
                os.replace(target + ".tmp", target)
                print(f"ok {d}: {len(glyphs)} glyphs in {time.time()-t0:.0f}s", file=sys.stderr)
            except Exception:
                print(f"FAIL {d}", file=sys.stderr)
                traceback.print_exc()
        sys.exit(0)


if __name__ == "__main__":
    FontExport().run()
