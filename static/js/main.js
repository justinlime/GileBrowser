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
   * Returns a score >= 0 if `pattern` fuzzy-matches `str`, or -1 otherwise.
   * A higher score indicates a better match (consecutive chars score higher).
   */
  function fuzzyScore(pattern, str) {
    pattern = pattern.toLowerCase();
    str = str.toLowerCase();

    if (pattern.length === 0) return 0;

    let score = 0;
    let pi = 0; // pattern index
    let lastMatch = -1;

    for (let si = 0; si < str.length && pi < pattern.length; si++) {
      if (str[si] === pattern[pi]) {
        // Bonus for consecutive characters
        score += (si === lastMatch + 1) ? 5 : 1;
        lastMatch = si;
        pi++;
      }
    }

    if (pi < pattern.length) return -1; // not all chars matched
    return score;
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

  function doSearch(query) {
    if (!fileIndex || query.trim() === "") {
      hideResults();
      return;
    }

    const scored = [];
    fileIndex.forEach(function (entry) {
      if (entry.dir) return; // skip directories, files only
      const score = fuzzyScore(query, entry.name);
      if (score >= 0) {
        scored.push({ score: score, name: entry.name, path: entry.path });
      }
    });

    scored.sort(function (a, b) { return b.score - a.score; });
    renderResults(scored);
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
    doSearch(searchInput.value);
  });

  searchInput.addEventListener("focus", function () {
    if (searchInput.value.trim()) {
      doSearch(searchInput.value);
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
