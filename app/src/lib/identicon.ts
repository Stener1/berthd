// A project with no GitHub owner gets an identicon on the rail, as GitHub
// gives an account with no picture: a 5×5 pattern mirrored down the middle,
// in one colour, worked out from the project's name so it never changes.
// No imports: pnpm test runs this file as it is.

export interface Identicon {
  // 25 cells, row by row: true where the colour goes.
  cells: boolean[];
  // The colour's hue, 0–359.
  hue: number;
}

// fnv1a is a small, stable hash of a string (32-bit FNV-1a).
function fnv1a(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

// identicon is the pattern and hue for a seed (a project's name). The left
// three columns come from the hash's low 15 bits and the right two mirror
// them; a pattern with almost nothing in it gets its middle column filled,
// so every tile reads as a mark.
export function identicon(seed: string): Identicon {
  const h = fnv1a(seed.trim().toLowerCase() || "?");
  const cells: boolean[] = new Array(25).fill(false);
  let on = 0;
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 3; col++) {
      const bit = ((h >>> (row * 3 + col)) & 1) === 1;
      cells[row * 5 + col] = bit;
      cells[row * 5 + (4 - col)] = bit;
      if (bit) on += col === 2 ? 1 : 2;
    }
  }
  if (on < 6) for (let row = 0; row < 5; row++) cells[row * 5 + 2] = true;
  return { cells, hue: (h >>> 16) % 360 };
}
