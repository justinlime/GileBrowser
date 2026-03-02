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

  // ------------------------------------------------------------------ //
  // Copy button functionality                                           //
  // ------------------------------------------------------------------ //

  (function () {
    /**
     * Show a temporary "Copied!" state on the button.
     */
    function showCopiedState(button, duration) {
      if (!duration) duration = 1500;
      var originalText = button.textContent;
      button.textContent = "Copied!";
      button.classList.add("copied");

      setTimeout(function () {
        button.textContent = originalText;
        button.classList.remove("copied");
      }, duration);
    }

    /**
     * Copy text to clipboard using the modern Clipboard API with fallback.
     */
    function copyToClipboard(text) {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        return navigator.clipboard.writeText(text).then(function () {
          return true;
        }).catch(function (err) {
          console.warn("Clipboard API failed, falling back:", err);
          return fallbackCopy(text);
        });
      } else {
        return fallbackCopy(text);
      }
    }

    /**
     * Fallback copy method using execCommand (deprecated but widely supported).
     */
    function fallbackCopy(text) {
      var textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.style.position = "fixed";
      textarea.style.left = "-9999px";
      textarea.style.top = "0";
      document.body.appendChild(textarea);
      textarea.select();

      try {
        var successful = document.execCommand("copy");
        document.body.removeChild(textarea);
        return Promise.resolve(successful);
      } catch (err) {
        document.body.removeChild(textarea);
        console.error("Fallback copy failed:", err);
        return Promise.reject(err);
      }
    }

    /**
     * Extract plain text from a Chroma syntax-highlighted table.
     * The table has two columns: line numbers and code content.
     */
    function extractTextFromChroma(chromaEl) {
      var rows = chromaEl.querySelectorAll("tr");
      var lines = [];

      rows.forEach(function (row) {
        var cells = row.querySelectorAll("td");
        if (cells.length > 1) {
          // Second column contains the actual code content
          var codeCell = cells[1];
          var text = codeCell.textContent;
          lines.push(text);
        } else if (cells.length === 1) {
          // Fallback: use the only cell's text
          lines.push(cells[0].textContent);
        }
      });

      return lines.join("\n");
    }

    /**
     * Set up copy button for full text preview.
     */
    (function () {
      var textPreview = document.querySelector(".text-preview");
      if (!textPreview) return;

      var chromaEl = textPreview.querySelector(".chroma");
      if (!chromaEl) return;

      // Create the copy button
      var copyBtn = document.createElement("button");
      copyBtn.className = "copy-btn";
      copyBtn.textContent = "Copy";
      copyBtn.type = "button";
      textPreview.appendChild(copyBtn);

      // Handle click
      copyBtn.addEventListener("click", function () {
        var textContent = extractTextFromChroma(chromaEl);
        copyToClipboard(textContent).then(function () {
          showCopiedState(copyBtn);
        }).catch(function (err) {
          console.error("Failed to copy:", err);
          copyBtn.textContent = "Error";
          setTimeout(function () {
            copyBtn.textContent = "Copy";
          }, 1500);
        });
      });
    })();

    /**
     * Set up copy buttons for code blocks in rendered content (Markdown/Org).
     */
    (function () {
      var renderedPreview = document.querySelector(".rendered-preview");
      if (!renderedPreview) return;

      // Find all pre elements that contain code (but not those already processed)
      var preElements = renderedPreview.querySelectorAll("pre:not(.has-copy-btn)");
      if (preElements.length === 0) return;

      preElements.forEach(function (pre) {
        // Mark as processed
        pre.classList.add("has-copy-btn");

        // Check if this is a Chroma-highlighted block or plain code
        var codeEl = pre.querySelector("code");
        if (!codeEl && !pre.querySelector(".chroma")) {
          return; // No code to copy
        }

        // Wrap content in a code-content div
        var codeContent = document.createElement("div");
        codeContent.className = "code-content";

        // Move all children (the actual code) into the wrapper
        while (pre.firstChild) {
          codeContent.appendChild(pre.firstChild);
        }

        // Create header with copy button
        var header = document.createElement("div");
        header.className = "code-header";

        var langLabel = document.createElement("span");
        langLabel.className = "code-lang";

        // Try to detect language from Chroma classes
        var chromaEl = pre.querySelector(".chroma");
        if (chromaEl) {
          // Look for language class like "language-python" or similar
          var codeInChroma = chromaEl.querySelector("code");
          if (codeInChroma) {
            var langMatch = codeInChroma.className.match(/language-(\w+)/);
            if (langMatch) {
              langLabel.textContent = langMatch[1];
            } else {
              // Check parent classes
              var classList = chromaEl.classList;
              for (var i = 0; i < classList.length; i++) {
                if (classList[i].startsWith("language-")) {
                  langLabel.textContent = classList[i].replace("language-", "");
                  break;
                }
              }
            }
          }
        }

        if (!langLabel.textContent) {
          langLabel.textContent = "code";
        }

        var copyCodeBtn = document.createElement("button");
        copyCodeBtn.className = "copy-code-btn";
        copyCodeBtn.textContent = "Copy";
        copyCodeBtn.type = "button";

        header.appendChild(langLabel);
        header.appendChild(copyCodeBtn);

        // Prepend header and add content wrapper back to pre
        pre.insertBefore(header, pre.firstChild);
        pre.appendChild(codeContent);

        // Handle click
        copyCodeBtn.addEventListener("click", function () {
          var textToCopy;

          if (chromaEl) {
            // Chroma-highlighted code - extract from table
            textToCopy = extractTextFromChroma(chromaEl);
          } else if (codeEl) {
            // Plain code block
            textToCopy = codeEl.textContent;
          } else {
            // Fallback: get all text from pre
            textToCopy = pre.textContent.replace(/\s*$/, "");
          }

          copyToClipboard(textToCopy).then(function () {
            showCopiedState(copyCodeBtn);
          }).catch(function (err) {
            console.error("Failed to copy:", err);
            copyCodeBtn.textContent = "Error";
            setTimeout(function () {
              copyCodeBtn.textContent = "Copy";
            }, 1500);
          });
        });
      });
    })();

  })();

})();
