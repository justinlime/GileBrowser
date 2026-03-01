/**
 * search-worker.js – fuzzy search runs entirely off the main thread.
 *
 * Protocol (main → worker):
 *   { type: "index",  data: [ {name, path, size}, … ] }
 *   { type: "search", query: "…", id: <number> }
 *
 * Protocol (worker → main):
 *   { id: <number>, results: [ {name, path, size, score}, … ] }
 *
 * The `id` field is a monotone counter managed by the caller.  The main
 * thread ignores any response whose id is older than the most-recently
 * dispatched one, so a slow search for an old query never clobbers a newer
 * result that already arrived.
 */
"use strict";

var index = [];

// ------------------------------------------------------------------ //
// Scoring                                                              //
// ------------------------------------------------------------------ //

function scoreToken(token, str) {
  if (token.length === 0) return 0;
  if (str.length === 0)   return -1;

  // Tier 1 – exact substring
  if (str.indexOf(token) !== -1) return 40;

  // Precompute word-boundary positions.
  var isBoundary = new Uint8Array(str.length);
  isBoundary[0] = 1;
  for (var i = 1; i < str.length; i++) {
    var c = str[i - 1];
    if (c === "/" || c === "-" || c === "_" || c === "." || c === " ") {
      isBoundary[i] = 1;
    }
  }

  // Tier 2 – acronym / boundary-only match
  var pi = 0;
  for (var si = 0; si < str.length && pi < token.length; si++) {
    if (isBoundary[si] && str[si] === token[pi]) pi++;
  }
  if (pi === token.length) return 20;

  // Tier 3 – standard fuzzy with consecutive / boundary bonuses
  var score = 0;
  pi = 0;
  var lastMatch = -1;
  for (var si = 0; si < str.length && pi < token.length; si++) {
    if (str[si] === token[pi]) {
      var bonus = 1;
      if (si === lastMatch + 1) bonus += 4;
      if (isBoundary[si])       bonus += 2;
      score += bonus;
      lastMatch = si;
      pi++;
    }
  }
  if (pi < token.length) return -1;

  return score * token.length / str.length;
}

function multiTokenScore(query, entry) {
  var tokens = query.toLowerCase().split(/\s+/).filter(function (t) {
    return t.length > 0;
  });
  if (tokens.length === 0) return -1;

  var nameLower = entry.name.toLowerCase();
  var pathLower = entry.path.toLowerCase();

  var total = 0;
  for (var i = 0; i < tokens.length; i++) {
    var tok  = tokens[i];
    var best = Math.max(scoreToken(tok, nameLower), scoreToken(tok, pathLower));
    if (best < 0) return -1;
    total += best;
  }
  return total;
}

// ------------------------------------------------------------------ //
// Message handler                                                      //
// ------------------------------------------------------------------ //

self.onmessage = function (e) {
  var msg = e.data;

  if (msg.type === "index") {
    index = msg.data;
    return;
  }

  if (msg.type === "search") {
    var query   = msg.query;
    var id      = msg.id;
    var scored  = [];

    for (var i = 0; i < index.length; i++) {
      var entry = index[i];
      var score = multiTokenScore(query, entry);
      if (score >= 0) {
        scored.push({ name: entry.name, path: entry.path, size: entry.size, score: score });
      }
    }

    scored.sort(function (a, b) { return b.score - a.score; });

    self.postMessage({ id: id, results: scored });
  }
};
