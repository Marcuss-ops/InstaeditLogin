#!/usr/bin/env node
/**
 * Generate web/public/favicon.ico (16x16 BGRA purple square) so the browser
 * stops requesting /favicon.ico in addition to /favicon.svg.
 *
 * Why we need this: index.html already declares <link rel="icon"
 * type="image/svg+xml" href="/favicon.svg"> which covers every modern
 * browser and crawler that respects the link tag — but a real subset of
 * clients (older headless browsers, some social previews, dev tools,
 * anything that hits /favicon.ico before parsing the HTML) STILL probes
 * the legacy /favicon.ico path. When the file isn't shipped, that probe
 * 404s and shows up as a red console error in the deployed app.
 *
 * Why not use a 3rd-party encoder (sharp / pngjs): the project has no
 * image-encode dependency today and an ICO of this size is 1.2 KB on
 * disk. Hand-rolling the BITMAPINFOHEADER + XOR + AND mask in this single
 * 100-line script is more honest than pulling in a 1 MB native dep for
 * one static asset. If a richer favicon (multi-resolution, real logo art)
 * becomes desirable, swap to an `sharp`-based generator and remove this.
 *
 * ICO format reference: an ICO is a 6-byte ICONDIR header + N
 * 16-byte ICONDIRENTRY directory entries + N images. Each image is a
 * BITMAPINFOHEADER (40 bytes) + XOR (top-down BGRA) + AND mask (1 bit per
 * pixel, padded to 4-byte row boundary). Height in BMPHEADER is doubled to
 * account for the AND mask trailing; some decoders expect that even when
 * the XOR has an alpha channel and the AND mask is all-zero.
 */
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(__dirname, "..");

const W = 16;
const H = 16;
// 32 bits per pixel → 4 bytes per pixel
const XOR_BYTES = W * H * 4;
// AND mask rows are padded to 4-byte boundaries. For W=16 that's
// ceil(16/8)=2 bytes padded to 4 = 4 bytes per row.
const AND_ROW_BYTES = Math.ceil(W / 32) * 4;
const AND_BYTES = AND_ROW_BYTES * H;
// Image data = BITMAPINFOHEADER + XOR + AND.
const IMG_BYTES = 40 + XOR_BYTES + AND_BYTES;
// File size = ICONDIR + 1 ICONDIRENTRY + image data.
const FILE_SIZE = 6 + 16 + IMG_BYTES;

const buf = Buffer.alloc(FILE_SIZE);
let off = 0;

// ---------------------------------------------------------------------------
// ICONDIR header (6 bytes)
// ---------------------------------------------------------------------------
buf.writeUInt16LE(0, off); // reserved, must be 0
off += 2;
buf.writeUInt16LE(1, off); // type: 1 = icon (vs 2 = cursor)
off += 2;
buf.writeUInt16LE(1, off); // image count
off += 2;

// ---------------------------------------------------------------------------
// ICONDIRENTRY (16 bytes) — one entry per image in the file
// ---------------------------------------------------------------------------
buf.writeUInt8(W, off++); // width  (0 means 256 — not our case)
buf.writeUInt8(H, off++); // height (0 means 256)
buf.writeUInt8(0, off++); // palette size (0 for >8 bpp)
buf.writeUInt8(0, off++); // reserved, must be 0
buf.writeUInt16LE(1, off); // color planes
off += 2;
buf.writeUInt16LE(32, off); // bits per pixel → 32-bit BGRA
off += 2;
buf.writeUInt32LE(IMG_BYTES, off); // size of just the image-data section
off += 4;
buf.writeUInt32LE(22, off); // offset from start of file to image-data start
off += 4;

// ---------------------------------------------------------------------------
// BITMAPINFOHEADER (40 bytes)
// ---------------------------------------------------------------------------
buf.writeUInt32LE(40, off); // size of this header
off += 4;
buf.writeInt32LE(W, off); // width
off += 4;
// ICO quirk: BITMAPINFOHEADER.height is the sum of XOR-height + AND-height,
// so it's doubled for an opaque/XOR-only image. Decoders expect this even
// when the AND mask is all-zero.
buf.writeInt32LE(H * 2, off); // height × 2
off += 4;
buf.writeUInt16LE(1, off); // color planes (must be 1)
off += 2;
buf.writeUInt16LE(32, off); // bits per pixel
off += 2;
buf.writeUInt32LE(0, off); // compression: 0 = BI_RGB (uncompressed)
off += 4;
buf.writeUInt32LE(XOR_BYTES, off); // image size (XOR only)
off += 4;
buf.writeInt32LE(0, off); // x pixels-per-meter
off += 4;
buf.writeInt32LE(0, off); // y pixels-per-meter
off += 4;
buf.writeUInt32LE(0, off); // colors used in palette (0 = max + 1)
off += 4;
buf.writeUInt32LE(0, off); // colors required (0 = all required)
off += 4;

// ---------------------------------------------------------------------------
// XOR pixel data — 16×16 BGRA, top-down, full canvas
// ---------------------------------------------------------------------------
// PLACEHOLDER COLOR: brand-purple-ish #6750A4 (Tailwind purple-500 family).
// Replace this with the real InstaEdit brand color once sourced from a
// design token or theme file. Flat fill keeps the favicon legible at 16x16
// without antialiasing. Regression test asserts geometry only, so the
// color can be iterated without touching tests.
// Brand-ish purple #6750A4 -> BGRA little-endian = (B=0xA4, G=0x50, R=0x67, A=0xFF)
const BLUE = 0xa4;
const GREEN = 0x50;
const RED = 0x67;
const ALPHA = 0xff;
for (let i = 0; i < W * H; i++) {
  buf.writeUInt8(BLUE, off++);
  buf.writeUInt8(GREEN, off++);
  buf.writeUInt8(RED, off++);
  buf.writeUInt8(ALPHA, off++);
}
// ---------------------------------------------------------------------------
// AND mask — all zero (assume the XOR alpha channel tells the truth).
// ---------------------------------------------------------------------------
for (let i = 0; i < AND_BYTES; i++) {
  buf.writeUInt8(0, off++);
}

if (off !== FILE_SIZE) {
  throw new Error(`Internal: wrote ${off} bytes but FILE_SIZE=${FILE_SIZE}`);
}

const outPath = resolve(projectRoot, "public", "favicon.ico");
writeFileSync(outPath, buf);
console.log(`Wrote ${buf.length} bytes to ${outPath}`);
