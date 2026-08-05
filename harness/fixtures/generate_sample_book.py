#!/usr/bin/env python3
"""Generate the sample epub fixture used by the book library harness.

Creates harness/fixtures/sample_book.epub — a valid EPUB with:
  * META-INF/container.xml → OEBPS/content.opf
  * dc:title / dc:creator metadata
  * an embedded cover image (OEBPS/cover.jpg)
"""
import io
import os
import zipfile
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "sample_book.epub")

CONTAINER = b"""<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>"""

OPF = """<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="BookId">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>The Blackhole Adventures</dc:title>
    <dc:creator opf:role="aut">Qtopie Team</dc:creator>
    <dc:language>en</dc:language>
    <meta name="cover" content="cover-image"/>
  </metadata>
  <manifest>
    <item id="cover-image" href="cover.jpg" media-type="image/jpeg"/>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>"""

CHAPTER = b"""<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>The Blackhole Adventures</title></head>
<body><h1>Chapter One</h1><p>Welcome to the Blackhole NAS book library.</p></body>
</html>"""


def make_cover() -> bytes:
    buf = io.BytesIO()
    img = Image.new("RGB", (600, 800), (18, 52, 96))
    # Simple decorative cover
    for i in range(0, 600, 6):
        for j in range(0, 800, 6):
            img.putpixel((i, j), (i % 255, (i + j) % 255, 120))
    img.save(buf, format="JPEG")
    return buf.getvalue()


def main():
    with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("mimetype", b"application/epub+zip")
        zf.writestr("META-INF/container.xml", CONTAINER)
        zf.writestr("OEBPS/content.opf", OPF.encode("utf-8"))
        zf.writestr("OEBPS/chapter.xhtml", CHAPTER)
        zf.writestr("OEBPS/cover.jpg", make_cover())
    print(f"Generated fixture: {OUT} ({os.path.getsize(OUT)} bytes)")


if __name__ == "__main__":
    main()
