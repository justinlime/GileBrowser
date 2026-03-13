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
        var chromaEl = pre.querySelector(".chroma");
        var codeEl = pre.querySelector("code");
        if (!codeEl && !chromaEl) {
          return; // No code to copy
        }

        // Wrap content in a code-content div
        var codeContent = document.createElement("div");
        codeContent.className = "code-content";

        // Move all children (the actual code) into the wrapper
        while (pre.firstChild) {
          codeContent.appendChild(pre.firstChild);
        }

        // Create empty header for layout (no language label)
        var header = document.createElement("div");
        header.className = "code-header";

        // Prepend header and add content wrapper back to pre
        pre.insertBefore(header, pre.firstChild);
        pre.appendChild(codeContent);

        // Create copy button positioned absolutely in top-right of pre
        var copyCodeBtn = document.createElement("button");
        copyCodeBtn.className = "copy-code-btn";
        copyCodeBtn.textContent = "Copy";
        copyCodeBtn.type = "button";
        pre.appendChild(copyCodeBtn);

        function handleCopy() {
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
        }

        // Handle click on button
        copyCodeBtn.addEventListener("click", function (e) {
          e.stopPropagation();
          handleCopy();
        });

        // On mobile, make entire header clickable
        header.addEventListener("click", function () {
          handleCopy();
        });
      });
    })();

  })();

})();

// ------------------------------------------------------------------ //
// Image Lightbox with Zoom                                           //
// ------------------------------------------------------------------ //

(function () {
  "use strict";

  var overlay = null;
  var container = null;
  var wrapper = null;
  var image = null;
  var closeBtn = null;
  var zoomInBtn = null;
  var zoomOutBtn = null;
  var zoomLevelDisplay = null;
  
  var currentZoom = 1;
  var minZoom = 1;
  var maxZoom = 8;
  var zoomStep = 0.25;
  var isPanning = false;
  var panStartX = 0;
  var panStartY = 0;
  var panOffsetX = 0;
  var panOffsetY = 0;

  // Create lightbox DOM elements
  function createLightbox() {
    overlay = document.createElement('div');
    overlay.className = 'image-lightbox-overlay';
    
    container = document.createElement('div');
    container.className = 'image-lightbox-container';
    
    wrapper = document.createElement('div');
    wrapper.className = 'image-lightbox-wrapper';
    
    image = document.createElement('img');
    image.className = 'image-lightbox-image';
    image.draggable = false;
    
    closeBtn = document.createElement('button');
    closeBtn.className = 'image-lightbox-close';
    closeBtn.innerHTML = '<svg viewBox="0 0 24 24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>';
    closeBtn.setAttribute('aria-label', 'Close');
    
    var controls = document.createElement('div');
    controls.className = 'image-lightbox-controls';
    
    zoomOutBtn = document.createElement('button');
    zoomOutBtn.className = 'image-lightbox-zoom-btn';
    zoomOutBtn.innerHTML = '<svg viewBox="0 0 24 24"><path d="M15.5 14h-7v-1.5h7V14zm3.5-5h-11c-.83 0-1.5-.67-1.5-1.5S6.17 6 7 6h11c.83 0 1.5.67 1.5 1.5s-.67 1.5-1.5 1.5z"/></svg>';
    zoomOutBtn.setAttribute('aria-label', 'Zoom out');
    
    zoomInBtn = document.createElement('button');
    zoomInBtn.className = 'image-lightbox-zoom-btn';
    zoomInBtn.innerHTML = '<svg viewBox="0 0 24 24"><path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z"/></svg>';
    zoomInBtn.setAttribute('aria-label', 'Zoom in');
    
    zoomLevelDisplay = document.createElement('span');
    zoomLevelDisplay.className = 'image-lightbox-zoom-level';
    zoomLevelDisplay.textContent = '100%';
    
    controls.appendChild(zoomOutBtn);
    controls.appendChild(zoomInBtn);
    controls.appendChild(zoomLevelDisplay);
    
    wrapper.appendChild(image);
    container.appendChild(wrapper);
    container.appendChild(closeBtn);
    container.appendChild(controls);
    overlay.appendChild(container);
    document.body.appendChild(overlay);

    // Event listeners
    closeBtn.addEventListener('click', closeLightbox);
    zoomInBtn.addEventListener('click', zoomIn);
    zoomOutBtn.addEventListener('click', zoomOut);
    
    // Click on image to toggle zoom
    image.addEventListener('click', function(e) {
      e.stopPropagation();
      // If mouse moved significantly, it was a drag, not a click - don't zoom
      if (mouseMoved) {
        mouseMoved = false;
        return;
      }
      if (currentZoom > minZoom && currentZoom <= 2) {
        resetZoom();
      } else {
        setZoom(currentZoom > 2 ? 2 : currentZoom + zoomStep);
      }
    });
    
    // Click on overlay closes lightbox
    overlay.addEventListener('click', function(e) {
      if (e.target === overlay || e.target === container) {
        resetZoom();
        closeLightbox();
      }
    });
    
    // Mouse wheel zoom
    container.addEventListener('wheel', handleWheel, { passive: false });
    
    // Pan functionality (mouse)
    overlay.addEventListener('mousedown', startPan);
    document.addEventListener('mousemove', pan);
    document.addEventListener('mouseup', endPan);
    
    // Touch events for mobile
    overlay.addEventListener('touchstart', handleTouchStart, { passive: true });
    overlay.addEventListener('touchmove', handleTouchMove, { passive: false });
    overlay.addEventListener('touchend', handleTouchEnd);
  }

  function openLightbox(imgSrc, altText) {
    if (!overlay) createLightbox();
    
    image.src = imgSrc;
    image.alt = altText || '';
    
    resetZoom();
    overlay.classList.add('active');
    document.body.style.overflow = 'hidden';
  }

  function closeLightbox() {
    if (!overlay) return;
    overlay.classList.remove('active');
    overlay.classList.remove('zoomed');
    image.classList.remove('zoomed');
    document.body.style.overflow = '';
  }

  function updateZoomDisplay() {
    var percentage = Math.round(currentZoom * 100);
    zoomLevelDisplay.textContent = percentage + '%';
  }

  function setZoom(zoom) {
    currentZoom = Math.max(minZoom, Math.min(maxZoom, zoom));
    // Preserve current pan offset when changing zoom
    wrapper.style.transform = 'translate(' + panOffsetX + 'px, ' + panOffsetY + 'px) scale(' + currentZoom + ')';
    updateZoomDisplay();
    
    if (currentZoom > minZoom) {
      overlay.classList.add('zoomed');
      image.classList.add('zoomed');
    } else {
      overlay.classList.remove('zoomed');
      image.classList.remove('zoomed');
    }
  }

  function zoomIn(e) {
    if (e) e.preventDefault();
    setZoom(currentZoom + zoomStep);
  }

  function zoomOut(e) {
    if (e) e.preventDefault();
    setZoom(currentZoom - zoomStep);
  }

  function resetZoom() {
    currentZoom = minZoom;
    panOffsetX = 0;
    panOffsetY = 0;
    touchPanningInitialized = false; // Reset for next pan gesture
    wrapper.style.transform = 'translate(0px, 0px) scale(' + minZoom + ')';
    updateZoomDisplay();
    overlay.classList.remove('zoomed');
    image.classList.remove('zoomed');
  }

  function handleWheel(e) {
    e.preventDefault();
    var delta = e.deltaY > 0 ? -1 : 1;
    setZoom(currentZoom + delta * zoomStep);
  }

  // Track mouse movement to distinguish click from drag
  var mouseMoved = false;
  var initialMouseX = 0;
  var initialMouseY = 0;

  // Mouse pan handlers
  function startPan(e) {
    if (currentZoom <= minZoom || e.button !== 0) return;
    isPanning = true;
    mouseMoved = false;
    panStartX = e.clientX - panOffsetX;
    panStartY = e.clientY - panOffsetY;
    initialMouseX = e.clientX;
    initialMouseY = e.clientY;
    overlay.classList.add('panning');
  }

  function pan(e) {
    if (!isPanning) return;
    // Check if mouse moved more than 3 pixels (to distinguish click from drag)
    var dx = Math.abs(e.clientX - initialMouseX);
    var dy = Math.abs(e.clientY - initialMouseY);
    if (dx > 3 || dy > 3) {
      mouseMoved = true;
    }
    e.preventDefault();
    panOffsetX = e.clientX - panStartX;
    panOffsetY = e.clientY - panStartY;
    wrapper.style.transform = 'translate(' + panOffsetX + 'px, ' + panOffsetY + 'px) scale(' + currentZoom + ')';
  }

  function endPan() {
    isPanning = false;
    if (overlay) overlay.classList.remove('panning');
  }

  // Touch handlers for mobile pinch and pan
  var touchStartDist = 0;
  var touchStartZoom = 1;
  var touchStartX = 0;
  var touchStartY = 0;
  var lastPanOffsetX = 0;
  var lastPanOffsetY = 0;
  var touchPanningInitialized = false;

  function handleTouchStart(e) {
    if (e.touches.length === 2) {
      // Pinch to zoom
      touchStartDist = getTouchDistance(e.touches);
      touchStartZoom = currentZoom;
      touchPanningInitialized = false; // Reset for next single-finger pan after pinch
    } else if (e.touches.length === 1 && currentZoom > minZoom) {
      // Single finger pan
      var touch = e.touches[0];
      touchStartX = touch.clientX - panOffsetX;
      touchStartY = touch.clientY - panOffsetY;
      lastPanOffsetX = panOffsetX;
      lastPanOffsetY = panOffsetY;
      touchPanningInitialized = true; // Already initialized on first movement
    }
  }

  function handleTouchMove(e) {
    if (e.touches.length === 2) {
      // Pinch zoom
      e.preventDefault();
      var currentDist = getTouchDistance(e.touches);
      var scale = currentDist / touchStartDist;
      setZoom(touchStartZoom * scale);
    } else if (e.touches.length === 1 && currentZoom > minZoom) {
      // Single finger pan
      e.preventDefault();
      var touch = e.touches[0];
      
      // If this is the first movement after switching from pinch, initialize pan position
      if (!touchPanningInitialized) {
        touchStartX = touch.clientX - panOffsetX;
        touchStartY = touch.clientY - panOffsetY;
        touchPanningInitialized = true;
      }
      
      panOffsetX = touch.clientX - touchStartX;
      panOffsetY = touch.clientY - touchStartY;
      wrapper.style.transform = 'translate(' + panOffsetX + 'px, ' + panOffsetY + 'px) scale(' + currentZoom + ')';
    }
  }

  function handleTouchEnd() {
    touchStartDist = 0;
    touchPanningInitialized = false; // Reset for next pan gesture
  }

  function getTouchDistance(touches) {
    var dx = touches[0].pageX - touches[1].pageX;
    var dy = touches[0].pageY - touches[1].pageY;
    return Math.sqrt(dx * dx + dy * dy);
  }

  // Keyboard controls
  document.addEventListener('keydown', function(e) {
    if (!overlay || !overlay.classList.contains('active')) return;
    
    switch (e.key) {
      case 'Escape':
        resetZoom();
        closeLightbox();
        break;
      case '+':
      case '=':
        zoomIn(e);
        break;
      case '-':
        zoomOut(e);
        break;
      case '0':
        e.preventDefault();
        resetZoom();
        break;
    }
  });

  // Make all images clickable for lightbox
  function makeImagesClickable() {
    var images = document.querySelectorAll('.image-preview img, .rendered-preview img');
    images.forEach(function(img) {
      if (!img.classList.contains('image-clickable')) {
        img.classList.add('image-clickable');
        img.addEventListener('click', function(e) {
          e.preventDefault();
          var src = this.getAttribute('src') || this.getAttribute('data-src');
          var alt = this.getAttribute('alt') || '';
          if (src) openLightbox(src, alt);
        });
      }
    });
  }

  // Initialize on page load
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', makeImagesClickable);
  } else {
    makeImagesClickable();
  }
})();
