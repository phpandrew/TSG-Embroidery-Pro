# Built-in lettering fonts

Each subfolder is one font, compiled into the program:

```
fonts/
  Varsity/
    font.json
    A.dst  B.dst  ... 0.dst ... 9.dst
```

`font.json`:

```json
{
  "name": "Varsity",
  "capHeight": 250,
  "glyphs": {
    "A": { "file": "A.dst", "advance": 230 },
    "Q": { "file": "Q.dst", "advance": 250, "descender": true }
  }
}
```

`capHeight` and `advance` are in 0.1 mm. Digitize each letter at the cap height, with underlay, in your current software and export DST.
Fonts placed in `%APPDATA%\TSG Embroidery Pro\fonts\` also load (per-PC, not compiled in).
