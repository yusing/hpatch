import {expect, test} from "bun:test";

import {decodeJavaScriptStringLiteral} from "../../../../plugins/javascript_string.ts";

const validBodies: Array<[string, string]> = [
  ["", ""],
  ["side-effect", "side-effect"],
  ["@scope/package", "@scope/package"],
  ["日本語/😀", "日本語/😀"],
  [String.raw`\'\"\\`, "'\"\\"],
  [String.raw`\b\f\n\r\t\v`, "\b\f\n\r\t\v"],
  [String.raw`\0`, "\0"],
  [String.raw`\0a`, "\0a"],
  [String.raw`\a\c\d\e\q\z\X\U\$\/\{\}`, "acdeqzXU$/{}"],
  [String.raw`\x00\x41\xAf`, "\0A¯"],
  [String.raw`\u0000\u0041\u00aF`, "\0A¯"],
  [String.raw`\uD83D\uDE00`, "😀"],
  [String.raw`\uD800\uDC00\uDFFF`, "\ud800\udc00\udfff"],
  [String.raw`\u{1F600}\u{10FFFF}`, "😀\u{10ffff}"],
  [String.raw`\u{0000000000000000000041}`, "A"],
  [String.raw`\u{D800}`, "\ud800"],
  ["a\\\nb", "ab"],
  ["a\\\rb", "ab"],
  ["a\\\r\nb", "ab"],
  ["a\\\u2028b\\\u2029c", "abc"],
  ["a\u2028b\u2029c", "a\u2028b\u2029c"],
  ["a\0\tb", "a\0\tb"],
  ["\\😀", "😀"],
];

for (const quote of ["'", '"']) {
  for (const [body, expected] of validBodies) {
    const literal = quote + body + quote;
    test(`decodes module string ${JSON.stringify(literal)}`, () => {
      expect(decodeJavaScriptStringLiteral(literal)).toBe(expected);
    });
  }
}

for (const literal of [
  "", "'", '"', "unquoted", "`template`", "'unclosed", '"mismatched\'',
  "'a'b'", '"a"b"', "'ok' tail", " 'ok'", "'trailing\\'",
  "'raw\nnewline'", "'raw\rcarriage'", "'a\\\n\rb'",
  String.raw`'\00'`, String.raw`'\01'`, String.raw`'\07'`, String.raw`'\08'`,
  String.raw`'\09'`, String.raw`'\1'`, String.raw`'\7'`, String.raw`'\8'`, String.raw`'\9'`,
  String.raw`'\x'`, String.raw`'\x1'`, String.raw`'\xgg'`,
  String.raw`'\u'`, String.raw`'\u123'`, String.raw`'\uzzzz'`,
  String.raw`'\u{}'`, String.raw`'\u{1'`, String.raw`'\u{110000}'`,
  String.raw`'\u{1_0000}'`, String.raw`'\u{+1}'`, String.raw`'\u{ 1}'`, String.raw`'\u{0x41}'`,
]) {
  test(`rejects invalid module string ${JSON.stringify(literal)}`, () => {
    expect(decodeJavaScriptStringLiteral(literal)).toBeNull();
  });
}
