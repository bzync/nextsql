// A Go string case list has one mandatory comma between labels. Keeping
// that separator explicit prevents overlapping whitespace repetitions from
// exponentially backtracking on malformed, space-separated quoted names.
export function goCaseNames(source) {
  return [...source.matchAll(/\bcase\s+("[a-z_][a-z_0-9]*"(?:\s*,\s*"[a-z_][a-z_0-9]*")*)\s*:/g)]
    .flatMap((match) => [...match[1].matchAll(/"([a-z_][a-z_0-9]*)"/g)].map((item) => item[1]));
}
