#!/usr/bin/env python3
"""Packs a set of PNG files into a single multi-resolution Windows .ico file
using PNG-compressed icon entries (supported since Windows Vista). Avoids
needing ImageMagick/other tools that aren't installed on this machine.

Usage: pack_ico.py out.ico size1.png size2.png ...
"""
import struct
import sys


def pack_ico(png_paths, out_path):
    images = []
    for path in png_paths:
        with open(path, "rb") as f:
            data = f.read()
        # Assumes square PNGs; read width from the IHDR chunk (bytes 16-20).
        width = struct.unpack(">I", data[16:20])[0]
        images.append((width, data))

    count = len(images)
    header = struct.pack("<HHH", 0, 1, count)

    entries = b""
    offset = 6 + 16 * count
    for width, data in images:
        dim = width if width < 256 else 0  # 0 means 256 in ICO format
        entry = struct.pack(
            "<BBBBHHII",
            dim, dim, 0, 0,  # width, height, colorCount, reserved
            1, 32,  # planes, bitCount
            len(data), offset,
        )
        entries += entry
        offset += len(data)

    with open(out_path, "wb") as f:
        f.write(header)
        f.write(entries)
        for _, data in images:
            f.write(data)


if __name__ == "__main__":
    out_path = sys.argv[1]
    png_paths = sys.argv[2:]
    pack_ico(png_paths, out_path)
    print(f"wrote {out_path} from {len(png_paths)} images")
