/**
 * main.js – client-side fuzzy search for GileBrowser.
 *
 * No external dependencies.  The server provides a JSON index at /api/index.
 * We fetch it once, then filter entirely in-browser.
 */

(function () {
  "use strict";

  // ------------------------------------------------------------------ //
  // Fuzzy matching                                                       //
  // ------------------------------------------------------------------ //

  /**
   * scoreToken(token, str) → number
   *
   * Scores how well a single lowercase token matches a lowercase string.
   * Returns -1 if the token cannot be matched at all.
   *
   * Scoring tiers (highest to lowest):
   *   40 – exact substring match
   *   20 – every character of the token appears at a word-boundary position
   *        (start of string, or after a separator: / - _ . space)
   *   1+ – standard fuzzy: characters appear in order, with a +4 bonus for
   *        each consecutive pair and a +2 bonus whenever a match lands on a
   *        word boundary
   *
   * A length-normalisation factor is applied so that a token that consumes
   * most of a short field outscores the same token scattered across a long one.
   */
  function scoreToken(token, str) {
    if (token.length === 0) return 0;
    if (str.length === 0) return -1;

    // Tier 1 – exact substring
    if (str.indexOf(token) !== -1) return 40;

    // Precompute which positions are word-boundary starts.
    // A boundary is: index 0, or the character after / - _ . or space.
    var isBoundary = new Uint8Array(str.length);
    isBoundary[0] = 1;
    for (var i = 1; i < str.length; i++) {
      var c = str[i - 1];
      if (c === "/" || c === "-" || c === "_" || c === "." || c === " ") {
        isBoundary[i] = 1;
      }
    }

    // Tier 2 – all token chars match at boundary positions (acronym style).
    // e.g. token "pom" matches "preview-org.md" via p→'p', o→'o', m→'m' all
    // at boundary positions.
    var pi = 0;
    for (var si = 0; si < str.length && pi < token.length; si++) {
      if (isBoundary[si] && str[si] === token[pi]) pi++;
    }
    if (pi === token.length) return 20;

    // Tier 3 – standard fuzzy with bonuses.
    var score = 0;
    pi = 0;
    var lastMatch = -1;
    for (var si = 0; si < str.length && pi < token.length; si++) {
      if (str[si] === token[pi]) {
        var bonus = 1;
        if (si === lastMatch + 1) bonus += 4; // consecutive
        if (isBoundary[si])       bonus += 2; // word boundary
        score += bonus;
        lastMatch = si;
        pi++;
      }
    }
    if (pi < token.length) return -1; // not all chars matched

    // Normalise: prefer matches where the token "fills" more of the field.
    return score * token.length / str.length;
  }

  /**
   * multiTokenScore(query, entry) → number
   *
   * Splits the query into whitespace-separated tokens and scores each one
   * against both the entry's filename (entry.name) and its full path
   * (entry.path).  Every token must match somewhere — if any token fails
   * to match both fields, the whole query scores -1 (no match).
   *
   * Each token contributes its best score (max of name-score and path-score),
   * and the total is the sum across all tokens so that matching more words
   * always wins over matching fewer.
   *
   * Examples:
   *   query "org preview"  → file "preview-org.md"
   *     token "org"     → scoreToken("org","preview-org.md") = exact → 40
   *     token "preview" → scoreToken("preview","preview-org.md") = exact → 40
   *     total = 80  ✓
   *
   *   query "drain notes"  → path "/example/notes/path/drain.md"
   *     token "drain" → name "drain.md"  = exact → 40
   *     token "notes" → path "…/notes/…" = exact → 40
   *     total = 80  ✓
   */
  function multiTokenScore(query, entry) {
    var tokens = query.toLowerCase().split(/\s+/).filter(function(t){ return t.length > 0; });
    if (tokens.length === 0) return -1;

    var nameLower = entry.name.toLowerCase();
    var pathLower = entry.path.toLowerCase();

    var total = 0;
    for (var i = 0; i < tokens.length; i++) {
      var tok = tokens[i];
      var ns = scoreToken(tok, nameLower);
      var ps = scoreToken(tok, pathLower);
      var best = Math.max(ns, ps);
      if (best < 0) return -1; // this token matched nowhere — reject entry
      total += best;
    }
    return total;
  }

  // ------------------------------------------------------------------ //
  // DOM helpers                                                          //
  // ------------------------------------------------------------------ //

  const searchInput = document.getElementById("search-input");
  const searchResults = document.getElementById("search-results");

  if (!searchInput || !searchResults) return;

  function showResults() {
    searchResults.classList.remove("hidden");
  }

  function hideResults() {
    searchResults.classList.add("hidden");
  }

  function renderResults(items) {
    searchResults.innerHTML = "";

    if (items.length === 0) {
      const el = document.createElement("div");
      el.className = "search-no-results";
      el.textContent = "No results found.";
      searchResults.appendChild(el);
      showResults();
      return;
    }

    items.slice(0, 40).forEach(function (item) {
      const a = document.createElement("a");
      a.href = "/preview" + item.path;
      a.className = "search-result-item";

      const name = document.createElement("span");
      name.className = "result-name";
      name.textContent = item.name;

      const path = document.createElement("span");
      path.className = "result-path";
      path.textContent = item.path;

      a.appendChild(name);
      a.appendChild(path);
      searchResults.appendChild(a);
    });

    showResults();
  }

  // ------------------------------------------------------------------ //
  // Index loading                                                        //
  // ------------------------------------------------------------------ //

  let fileIndex = null; // Array of {name, path, dir}

  function loadIndex() {
    fetch("/api/index")
      .then(function (r) { return r.json(); })
      .then(function (data) {
        fileIndex = data.files || [];
      })
      .catch(function (err) {
        console.warn("GileBrowser: failed to load search index:", err);
      });
  }

  // ------------------------------------------------------------------ //
  // Search                                                               //
  // ------------------------------------------------------------------ //

  // Pending debounce timer id.  Any keystroke resets it so the search only
  // runs once the user pauses for DEBOUNCE_MS — keeping the input responsive
  // no matter how large the index is.
  var searchTimer = null;
  var DEBOUNCE_MS = 120;

  function doSearch(query) {
    if (!fileIndex || query.trim() === "") {
      hideResults();
      return;
    }

    var scored = [];
    fileIndex.forEach(function (entry) {
      var score = multiTokenScore(query, entry);
      if (score >= 0) {
        scored.push({ score: score, name: entry.name, path: entry.path });
      }
    });

    scored.sort(function (a, b) { return b.score - a.score; });
    renderResults(scored);
  }

  function scheduleSearch(query) {
    clearTimeout(searchTimer);
    if (!query.trim()) {
      hideResults();
      return;
    }
    searchTimer = setTimeout(function () { doSearch(query); }, DEBOUNCE_MS);
  }

  // ------------------------------------------------------------------ //
  // Keyboard navigation                                                  //
  // ------------------------------------------------------------------ //

  let activeIdx = -1;

  function setActive(idx) {
    const items = searchResults.querySelectorAll(".search-result-item");
    items.forEach(function (el, i) {
      el.classList.toggle("active", i === idx);
    });
    activeIdx = idx;
  }

  searchInput.addEventListener("keydown", function (e) {
    const items = searchResults.querySelectorAll(".search-result-item");
    const count = items.length;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive(Math.min(activeIdx + 1, count - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(Math.max(activeIdx - 1, 0));
    } else if (e.key === "Enter") {
      if (activeIdx >= 0 && items[activeIdx]) {
        items[activeIdx].click();
      }
    } else if (e.key === "Escape") {
      hideResults();
      searchInput.blur();
    }
  });

  // ------------------------------------------------------------------ //
  // Event wiring                                                         //
  // ------------------------------------------------------------------ //

  searchInput.addEventListener("input", function () {
    activeIdx = -1;
    scheduleSearch(searchInput.value);
  });

  searchInput.addEventListener("focus", function () {
    if (searchInput.value.trim()) {
      scheduleSearch(searchInput.value);
    }
  });

  document.addEventListener("click", function (e) {
    if (!searchInput.contains(e.target) && !searchResults.contains(e.target)) {
      hideResults();
    }
  });

  // Load index on page load
  loadIndex();

  // ------------------------------------------------------------------ //
  // Theme toggle (Catppuccin Mocha dark / Latte light)                  //
  // ------------------------------------------------------------------ //

  (function () {
    var html = document.documentElement;
    var btn  = document.getElementById("theme-toggle");
    var icon = document.getElementById("theme-icon");

    // Server-configured default, injected via data-default-theme on <html>.
    var serverDefault = html.getAttribute("data-default-theme") || "dark";

    // Priority: 1) client localStorage pref  2) server default
    var saved = localStorage.getItem("gile-theme");
    var current = saved || serverDefault;

    function applyTheme(theme) {
      current = theme;
      html.classList.remove("dark", "light");
      html.classList.add(theme);
      if (icon) {
        icon.textContent = theme === "dark" ? "☀" : "☽";
      }
      localStorage.setItem("gile-theme", theme);
    }

    // Apply immediately to avoid flash
    applyTheme(current);

    if (btn) {
      btn.addEventListener("click", function () {
        applyTheme(current === "dark" ? "light" : "dark");
      });
    }
  })();

  // ------------------------------------------------------------------ //
  // Global keyboard shortcut: press "/" to focus search                 //
  // ------------------------------------------------------------------ //
  document.addEventListener("keydown", function (e) {
    if (
      e.key === "/" &&
      document.activeElement !== searchInput &&
      document.activeElement.tagName !== "INPUT" &&
      document.activeElement.tagName !== "TEXTAREA"
    ) {
      e.preventDefault();
      searchInput.focus();
    }
  });

  // ------------------------------------------------------------------ //
  // Row click handling (all screen sizes)                              //
  // ------------------------------------------------------------------ //
  (function () {
    var fileTable = document.querySelector(".file-table");
    if (!fileTable) return;

    fileTable.addEventListener("click", function (e) {
      // Find the closest row (tr) element
      var row = e.target.closest("tr");
      if (!row) return;

      // Find the entry link in this row
      var entryLink = row.querySelector(".entry-link");
      if (!entryLink) return;

      // If user clicked directly on a button, don't trigger row click
      if (e.target.closest(".btn")) {
        return;
      }

      // Navigate to the entry link's URL
      window.location.href = entryLink.href;
    });
  })();
})();
