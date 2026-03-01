/**
 * main.js – GileBrowser UI.
 *
 * Search is delegated entirely to search-worker.js so the scoring loop
 * never runs on the main thread and cannot stall keyboard input.
 */

(function () {
  "use strict";

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

  function humanSize(bytes) {
    if (bytes === 0) return "0 B";
    if (bytes < 1024) return bytes + " B";
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
    return (bytes / (1024 * 1024 * 1024)).toFixed(1) + " GB";
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

      // Top row: filename + size chip
      const top = document.createElement("span");
      top.className = "result-top";

      const name = document.createElement("span");
      name.className = "result-name";
      name.textContent = item.name;
      top.appendChild(name);

      if (item.size != null && item.size >= 0) {
        const size = document.createElement("span");
        size.className = "result-size";
        size.textContent = "(" + humanSize(item.size) + ")";
        top.appendChild(size);
      }

      // Bottom row: path
      const path = document.createElement("span");
      path.className = "result-path";
      path.textContent = item.path;

      a.appendChild(top);
      a.appendChild(path);
      searchResults.appendChild(a);
    });

    showResults();
  }

  // ------------------------------------------------------------------ //
  // Search worker                                                        //
  // ------------------------------------------------------------------ //

  // Monotone counter — incremented on every query dispatch.  The worker
  // echoes the id back with its response; any response whose id is less
  // than lastId was superseded by a newer query and is discarded without
  // touching the DOM.
  var lastId = 0;
  var worker = new Worker("/static/js/search-worker.js");

  worker.onmessage = function (e) {
    // Discard results that belong to a superseded query.
    if (e.data.id < lastId) return;
    renderResults(e.data.results);
  };

  worker.onerror = function (err) {
    console.warn("GileBrowser: search worker error:", err.message);
  };

  function dispatchSearch(query) {
    if (!query.trim()) {
      hideResults();
      return;
    }
    lastId++;
    worker.postMessage({ type: "search", query: query, id: lastId });
  }

  // ------------------------------------------------------------------ //
  // Index loading                                                        //
  // ------------------------------------------------------------------ //

  function loadIndex() {
    fetch("/api/index")
      .then(function (r) { return r.json(); })
      .then(function (data) {
        // Hand the entire index to the worker once — it keeps it in memory
        // for all subsequent queries without any main-thread involvement.
        worker.postMessage({ type: "index", data: data.files || [] });
      })
      .catch(function (err) {
        console.warn("GileBrowser: failed to load search index:", err);
      });
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
    dispatchSearch(searchInput.value);
  });

  searchInput.addEventListener("focus", function () {
    if (searchInput.value.trim()) {
      dispatchSearch(searchInput.value);
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
  // Row click handling (all screen sizes)                               //
  // ------------------------------------------------------------------ //

  (function () {
    var fileTable = document.querySelector(".file-table");
    if (!fileTable) return;

    fileTable.addEventListener("click", function (e) {
      var row = e.target.closest("tr");
      if (!row) return;

      var entryLink = row.querySelector(".entry-link");
      if (!entryLink) return;

      if (e.target.closest(".btn")) return;

      window.location.href = entryLink.href;
    });
  })();

})();
