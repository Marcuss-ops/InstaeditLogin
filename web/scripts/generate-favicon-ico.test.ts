import { describe, it, expect } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * Regression test for the hand-rolled ICO writer at
 * scripts/generate-favicon-ico.mjs. The point of this file is to catch
 * two specific failure modes:
 *
 *   1. Silent drift — someone tweaks the constants in the generator (W, H,
 *      BPP, color literals) and the committed favicon.ico gets out of sync
 *      because there's no assertion pinning the contract.
 *   2. Format regression — Node Buffer API changes (signed/unsigned
 *      defaults, BigInt interop) or a refactor accidentally shifts the
 *      header geometry and modern browsers stop recognizing the file.
 *
 * Asserts file geometry only: size + ICONDIR header + per-image entry +
 * data offset. The pixel data itself (color, alpha) is intentionally NOT
 * tested — that's a stylistic decision the generator's author can iterate
 * on without breaking the test.
 */

const __dirname = dirname(fileURLToPath(import.meta.url));
// __dirname here is /web/scripts; ICO lives at /web/public/favicon.ico.
const ICO_PATH = resolve(__dirname, "..", "public", "favicon.ico");

// Constants mirrored from the generator so the test errors mention the
// actual numbers, not magic literals.
const ICON_DIR_BYTES = 6; // ICONDIR header
const ICON_DIR_ENTRY_BYTES = 16; // ICONDIRENTRY per image
const BMP_HEADER_BYTES = 40; // BITMAPINFOHEADER
const XOR_BYTES = 16 * 16 * 4; // 32-bit BGRA, 16x16 pixels
const AND_MASK_BYTES = Math.ceil(16 / 32) * 4 * 16; // padded to 4-byte row boundary
const HEADER_BYTES = ICON_DIR_BYTES + ICON_DIR_ENTRY_BYTES; // 22
const EXPECTED_SIZE = HEADER_BYTES + BMP_HEADER_BYTES + XOR_BYTES + AND_MASK_BYTES;

describe("public/favicon.ico", () => {
  it("exists (pretest/prebuild hook writes it on every test/build run)", () => {
    expect(existsSync(ICO_PATH)).toBe(true);
  });

  it(`is exactly ${EXPECTED_SIZE} bytes (ICONDIR 6 + ICONDIRENTRY 16 + BITMAPINFOHEADER 40 + XOR 1024 + AND 64)`, () => {
    const buf = readFileSync(ICO_PATH);
    expect(buf.length).toBe(EXPECTED_SIZE);
  });

  it("ICONDIR header is well-formed", () => {
    const b = readFileSync(ICO_PATH);
    expect(b.readUInt16LE(0)).toBe(0); // reserved, must be 0
    expect(b.readUInt16LE(2)).toBe(1); // type 1 = icon
    expect(b.readUInt16LE(4)).toBe(1); // 1 image present
  });

  it("ICONDIRENTRY declares 16x16 32-bpp image", () => {
    const b = readFileSync(ICO_PATH);
    expect(b.readUInt8(6)).toBe(16); // width (0 = 256, 16 is literal)
    expect(b.readUInt8(7)).toBe(16); // height
    expect(b.readUInt8(8)).toBe(0); // palette size (0 for >8bpp)
    expect(b.readUInt8(9)).toBe(0); // reserved
    expect(b.readUInt16LE(10)).toBe(1); // color planes
    expect(b.readUInt16LE(12)).toBe(32); // bits per pixel (BGRA)
  });

  it("image-data section starts at offset 22 (right after ICONDIR + ICONDIRENTRY)", () => {
    const b = readFileSync(ICO_PATH);
    expect(b.readUInt32LE(18)).toBe(22);
  });

  it("image-data size and BITMAPINFOHEADER geometry are consistent", () => {
    const b = readFileSync(ICO_PATH);
    // ICONDIRENTRY.size = image-data bytes = file length - HEADER_BYTES.
    // Already implied by the size assertion above, but pinned here so a
    // future header-shape change gets caught instead of silently drifting
    // the geometry.
    expect(b.readUInt32LE(14)).toBe(b.length - HEADER_BYTES);
    // BITMAPINFOHEADER.size must be 40 (BITMAPINFOHEADER, not BITMAPCOREHEADER).
    expect(b.readUInt32LE(22)).toBe(40);
    // Width and height doubled (ICO quirk to include AND mask).
    expect(b.readInt32LE(26)).toBe(16); // width
    expect(b.readInt32LE(30)).toBe(32); // height × 2
    // Compression must be BI_RGB (0).
    expect(b.readUInt32LE(38)).toBe(0);
  });
});
