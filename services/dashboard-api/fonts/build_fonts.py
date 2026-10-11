"""Rebuild the two fonts embedded in ../static/index.html.

Downloads the Latin variable fonts from Google Fonts, keeps only the characters and axis
ranges the page uses, and writes them as base64 into the page's @font-face rules.

    pip install fonttools brotli
    python3 services/dashboard-api/fonts/build_fonts.py

Only needed when the character set or the fonts change; the page itself has no build step.
"""
import base64
import io
import pathlib
import re
import urllib.request

from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

PAGE = pathlib.Path(__file__).resolve().parent.parent / "static" / "index.html"
CSS_API = "https://fonts.googleapis.com/css2?family={family}&display=swap"
UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

# Basic Latin plus the punctuation, currency and arrows the page prints.
UNICODES = list(range(0x20, 0x7F)) + [
    0xA0, 0xA3, 0xA9, 0xAE, 0xB0, 0xB1, 0xB7, 0xBD, 0xD7, 0xE9, 0x2009, 0x2013, 0x2014, 0x2018,
    0x2019, 0x201C, 0x201D, 0x2022, 0x2026, 0x202F, 0x2032, 0x2033, 0x2044, 0x20AC, 0x2116,
    0x2190, 0x2191, 0x2192, 0x2193, 0x2212,
]

FONTS = [
    # (family in the CSS rule, Google Fonts query, axis limits, OpenType features kept)
    ("Archivo", "Archivo:wdth,wght@62..125,100..900", {"wght": (300, 800), "wdth": (100, 125)},
     ["kern", "liga", "tnum", "lnum", "case", "calt", "ss01"]),
    ("JetBrains Mono", "JetBrains+Mono:wght@100..800", {"wght": (400, 700)},
     ["kern", "tnum", "zero", "calt"]),
]


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    with urllib.request.urlopen(req) as r:
        return r.read()


def latin_woff2(query):
    css = fetch(CSS_API.format(family=query)).decode()
    # The last block is the "latin" subset.
    return fetch(re.findall(r"src: url\((https://[^)]+\.woff2)\)", css)[-1])


def build(data, limits, features):
    opts = subset.Options()
    opts.layout_features = features
    opts.name_IDs = ["*"]  # keeps the copyright and licence names
    opts.hinting = False
    opts.notdef_outline = True
    font = TTFont(io.BytesIO(data), lazy=False)
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=UNICODES)
    sub.subset(font)
    flat = io.BytesIO()
    font.flavor = None
    font.save(flat)
    font = instancer.instantiateVariableFont(TTFont(io.BytesIO(flat.getvalue()), lazy=False), limits)
    out = io.BytesIO()
    font.flavor = "woff2"
    font.save(out)
    return out.getvalue()


def main():
    page = PAGE.read_text()
    for family, query, limits, features in FONTS:
        woff2 = build(latin_woff2(query), limits, features)
        rule = re.compile(r'(@font-face\{font-family:"' + re.escape(family) + r'";[^}]*?base64,)[A-Za-z0-9+/=]+')
        page, n = rule.subn(lambda m: m.group(1) + base64.b64encode(woff2).decode(), page)
        if n != 1:
            raise SystemExit(f"could not find the @font-face rule for {family}")
        print(f"{family}: {len(woff2)} bytes")
    PAGE.write_text(page)


if __name__ == "__main__":
    main()