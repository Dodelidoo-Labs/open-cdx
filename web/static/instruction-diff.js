(function (root) {
  // Myers line diff with a work limit. Large rewrites fall back to complete
  // removal/addition blocks; content is never dropped to meet the work limit.
  function diff(before, after, budget = 200000) {
    const split = (text) => text === '' ? [] : text.split('\n');
    const a = split(before), b = split(after);
    let start = 0, end = 0;
    while (start < a.length && start < b.length && a[start] === b[start]) start++;
    while (end < a.length - start && end < b.length - start && a[a.length - end - 1] === b[b.length - end - 1]) end++;
    const left = a.slice(start, a.length - end), right = b.slice(start, b.length - end);
    const prefix = a.slice(0, start).map(text => ({kind:'same', text}));
    const suffix = a.slice(a.length - end).map(text => ({kind:'same', text}));
    let middle = [], coarse = false;
    if (!left.length) middle = right.map(text => ({kind:'add', text}));
    else if (!right.length) middle = left.map(text => ({kind:'remove', text}));
    else {
      let frontier = new Map([[1, 0]]), work = 0, finished = false;
      const trace = [];
      outer: for (let depth = 0; depth <= left.length + right.length; depth++) {
        trace.push(new Map(frontier));
        for (let diagonal = -depth; diagonal <= depth; diagonal += 2) {
          if (++work > budget) break outer;
          let x = diagonal === -depth || (diagonal !== depth && (frontier.get(diagonal - 1) ?? -1) < (frontier.get(diagonal + 1) ?? -1))
            ? (frontier.get(diagonal + 1) || 0) : (frontier.get(diagonal - 1) || 0) + 1;
          let y = x - diagonal;
          while (x < left.length && y < right.length && left[x] === right[y]) { x++; y++; if (++work > budget) break outer; }
          frontier.set(diagonal, x);
          if (x >= left.length && y >= right.length) {
            let currentX = left.length, currentY = right.length;
            for (let d = trace.length - 1; d >= 0; d--) {
              const v = trace[d], k = currentX - currentY;
              const previousK = k === -d || (k !== d && (v.get(k - 1) ?? -1) < (v.get(k + 1) ?? -1)) ? k + 1 : k - 1;
              const previousX = v.get(previousK) || 0, previousY = previousX - previousK;
              while (currentX > previousX && currentY > previousY) { middle.push({kind:'same', text:left[--currentX]}); currentY--; }
              if (d > 0) {
                if (currentX === previousX) middle.push({kind:'add', text:right[--currentY]});
                else middle.push({kind:'remove', text:left[--currentX]});
              }
            }
            middle.reverse(); finished = true; break outer;
          }
        }
      }
      if (!finished) { coarse = true; middle = [...left.map(text => ({kind:'remove',text})), ...right.map(text => ({kind:'add',text}))]; }
    }
    let oldLine = 0, newLine = 0;
    const lines = [...prefix, ...middle, ...suffix].map(line => ({...line, oldLine:line.kind === 'add' ? null : ++oldLine, newLine:line.kind === 'remove' ? null : ++newLine}));
    return {lines, coarse};
  }
  function context(lines, surrounding = 3) {
    const show = new Uint8Array(lines.length);
    lines.forEach((line, i) => {
      if (line.kind !== 'same') for (let j = Math.max(0, i - surrounding); j <= Math.min(lines.length - 1, i + surrounding); j++) show[j] = 1;
    });
    const result = [];
    for (let i = 0; i < lines.length;) {
      if (show[i]) { result.push(lines[i++]); continue; }
      let end = i; while (end < lines.length && !show[end]) end++;
      result.push({kind:'gap', count:end - i}); i = end;
    }
    return result;
  }
  const api = {diff, context};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.OpenCDXInstructionDiff = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
