const characterEscapes: Readonly<Record<string, string>> = {
  b: "\b", f: "\f", n: "\n", r: "\r", t: "\t", v: "\v",
};

// ECMAScript StringLiteral/SV, with the strict-mode rules required by modules:
// https://tc39.es/ecma262/multipage/ecmascript-language-lexical-grammar.html#sec-literals-string-literals
export function decodeJavaScriptStringLiteral(literal: string): string | null {
  const quote = literal[0];
  const end = literal.length - 1;
  if ((quote !== "'" && quote !== '"') || end < 1 || literal[end] !== quote) {
    return null;
  }
  let value = "";
  for (let index = 1; index < end; index += 1) {
    const character = literal[index];
    if (character === quote || character === "\n" || character === "\r") {
      return null;
    }
    if (character !== "\\") {
      value += character;
      continue;
    }
    index += 1;
    if (index >= end) {
      return null;
    }
    const escaped = literal[index];
    if (escaped === "\r" || escaped === "\n" || escaped === "\u2028" || escaped === "\u2029") {
      if (escaped === "\r" && literal[index + 1] === "\n") {
        index += 1;
      }
      continue;
    }
    if (escaped >= "0" && escaped <= "9") {
      if (escaped !== "0" || /[0-9]/u.test(literal[index + 1])) {
        return null;
      }
      value += "\0";
      continue;
    }
    if (escaped === "x" || escaped === "u") {
      const braced = escaped === "u" && literal[index + 1] === "{";
      const start = index + (braced ? 2 : 1);
      const finish = braced ? literal.indexOf("}", start) : start + (escaped === "x" ? 2 : 4);
      if (finish < start || finish > end || (braced && finish === end)) {
        return null;
      }
      const digits = literal.slice(start, finish);
      if (!/^[0-9a-fA-F]+$/u.test(digits)) {
        return null;
      }
      const codePoint = Number.parseInt(digits, 16);
      if (codePoint > 0x10ffff) {
        return null;
      }
      value += String.fromCodePoint(codePoint);
      index = braced ? finish : finish - 1;
      continue;
    }
    // Quotes, reverse solidus, and identity escapes retain the escaped character.
    value += characterEscapes[escaped] ?? escaped;
  }
  return value;
}
