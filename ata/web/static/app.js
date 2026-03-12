// Update the count badges next to column headers based on actual task rows.
function updateColumnCounts() {
    document.querySelectorAll('.task-list').forEach(function(list) {
        var count = list.querySelectorAll('.task-row').length;
        var heading = list.parentElement.querySelector('h2 .count');
        if (heading) heading.textContent = count;
    });
}

document.addEventListener('DOMContentLoaded', function() {
    // Suppress SSE reloads briefly after local actions to avoid self-triggered reloads.
    window._localActionUntil = 0;

    // Sortable.js on drag-to-reorder lists.
    // Queue and backlog share a group so tasks can be dragged between them.
    document.querySelectorAll('.sortable').forEach(function(el) {
        new Sortable(el, {
            group: 'tasks',
            animation: 150,
            delay: 300,
            delayOnTouchOnly: true,
            ghostClass: 'sortable-ghost',
            chosenClass: 'sortable-chosen',
            onEnd: function(evt) {
                var id = evt.item.dataset.id;
                var targetStatus = evt.to.dataset.status;
                var body = 'id=' + encodeURIComponent(id) + '&position=' + evt.newIndex;
                if (targetStatus) {
                    body += '&status=' + encodeURIComponent(targetStatus);
                }
                window._localActionUntil = Date.now() + 1000;
                fetch('/reorder', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/x-www-form-urlencoded'},
                    body: body
                });
                updateColumnCounts();
            }
        });
    });

    // SSE: reload page when tasks change externally (e.g. CLI moves).
    var sseEl = document.querySelector('[data-sse-workspace]');
    if (sseEl) {
        var ws = sseEl.getAttribute('data-sse-workspace');
        var es = new EventSource('/events?workspace=' + encodeURIComponent(ws));
        var reloadTimer = null;
        ['task_created', 'task_updated', 'task_closed', 'task_reordered'].forEach(function(evt) {
            es.addEventListener(evt, function() {
                if (Date.now() < window._localActionUntil) return;
                clearTimeout(reloadTimer);
                reloadTimer = setTimeout(function() { window.location.reload(); }, 300);
            });
        });
        // Close SSE before navigating away to free the HTTP connection slot.
        // Browsers limit ~6 connections per origin; stale SSE connections
        // from rapid navigation can exhaust this limit.
        window.addEventListener('beforeunload', function() { es.close(); });
    }

    // Quick-add textarea: Enter to submit, Shift+Enter for newline.
    var quickAdd = document.querySelector('.quick-add-input');
    if (quickAdd) {
        quickAdd.addEventListener('keydown', function(e) {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                var val = quickAdd.value.trim();
                if (val) quickAdd.closest('form').requestSubmit();
            }
        });
    }

    // Chip inputs for tag entry.
    initChipInputs();
    initInlineTagForms();
    initTagFormSSESuppression();

    // Body inline editing on task detail page.
    var bodyDisplay = document.getElementById('body-display');
    var bodyEdit = document.getElementById('body-edit');
    if (bodyDisplay && bodyEdit) {
        bodyDisplay.addEventListener('click', function() {
            bodyDisplay.style.display = 'none';
            bodyEdit.style.display = 'block';
            var textarea = bodyEdit.querySelector('textarea');
            textarea.focus();
            // Move cursor to end.
            textarea.selectionStart = textarea.value.length;
        });

        // After saving, reload to show rendered markdown.
        bodyEdit.addEventListener('htmx:afterRequest', function(e) {
            if (e.detail.successful) window.location.reload();
        });
    }
});

// Deterministic tag hue matching the Go tagHue() implementation.
// Uses FNV-1a hash into a curated palette avoiding purple (reserved for epics).
function tagHue(tag) {
    var palette = [0, 28, 55, 85, 125, 165, 195, 220, 320, 345];
    tag = tag.toLowerCase();
    var h = 0x811c9dc5;
    for (var i = 0; i < tag.length; i++) {
        h ^= tag.charCodeAt(i);
        h = Math.imul(h, 0x01000193) >>> 0;
    }
    return palette[h % palette.length];
}

// --- Chip input for quick-add tag entry ---
function initChipInputs() {
    document.querySelectorAll('.chip-input-row').forEach(function(row) {
        var form = row.closest('form');
        var hiddenInput = form.querySelector('input[name="tags"]');
        var chipsContainer = row.querySelector('.chip-input-chips');
        var field = row.querySelector('.chip-input-field');
        var tags = [];

        function renderChips() {
            chipsContainer.innerHTML = '';
            tags.forEach(function(t, i) {
                var hue = tagHue(t);
                var chip = document.createElement('span');
                chip.className = 'chip-input-chip';
                chip.style.color = 'hsl(' + hue + ', 70%, 75%)';
                chip.style.background = 'hsl(' + hue + ', 50%, 18%)';
                chip.textContent = t;
                var btn = document.createElement('button');
                btn.type = 'button';
                btn.className = 'chip-remove';
                btn.textContent = '\u00d7';
                btn.addEventListener('click', function() {
                    tags.splice(i, 1);
                    renderChips();
                });
                chip.appendChild(btn);
                chipsContainer.appendChild(chip);
            });
            hiddenInput.value = tags.join(',');
        }

        function addTag(value) {
            var t = value.toLowerCase().trim();
            if (t && tags.indexOf(t) === -1) {
                tags.push(t);
                renderChips();
            }
            field.value = '';
        }

        field.addEventListener('keydown', function(e) {
            if (e.key === 'Enter' || e.key === ',' || e.key === 'Tab') {
                if (field.value.trim()) {
                    e.preventDefault();
                    addTag(field.value);
                }
            }
            if (e.key === 'Backspace' && field.value === '' && tags.length > 0) {
                tags.pop();
                renderChips();
            }
        });

        // Handle datalist selection (input event fires when picking from dropdown).
        field.addEventListener('input', function() {
            var val = field.value;
            if (val.indexOf(',') >= 0) {
                val.split(',').forEach(function(v) { addTag(v); });
            }
        });

        // Commit pending tag text on datalist selection via change event.
        field.addEventListener('change', function() {
            if (field.value.trim()) addTag(field.value);
        });

        // Commit any pending tag text before the form submits.
        form.addEventListener('submit', function() {
            if (field.value.trim()) addTag(field.value);
        });

        // Reset chips when form submits successfully.
        if (form.getAttribute('hx-post')) {
            form.addEventListener('htmx:afterRequest', function(e) {
                if (e.detail.successful) {
                    tags = [];
                    renderChips();
                }
            });
        }
    });
}

// --- Inline tag add on detail and workspace pages (Enter to submit) ---
function initInlineTagForms() {
    document.querySelectorAll('.tag-add-inline').forEach(function(form) {
        var field = form.querySelector('.chip-input-field');
        field.addEventListener('keydown', function(e) {
            if (e.key === 'Enter') {
                e.preventDefault();
                var val = field.value.toLowerCase().trim();
                if (val) {
                    field.value = val;
                    form.requestSubmit();
                }
            }
        });
    });
}

// Suppress SSE reloads when tag forms submit (htmx).
// Uses event delegation so it works for any number of forms without O(N) setup.
function initTagFormSSESuppression() {
    document.body.addEventListener('htmx:beforeRequest', function(e) {
        if (e.target.closest('.tag-add-compact, .tag-remove-inline')) {
            window._localActionUntil = Date.now() + 1000;
        }
    });
}

// Called by Cancel button on body edit form.
function cancelBodyEdit() {
    document.getElementById('body-display').style.display = '';
    document.getElementById('body-edit').style.display = 'none';
}

// Tag filter: click pill to toggle include, click "−" to toggle exclude.
// Uses window.location so non-tag params (show_closed, path, etc.) are preserved.
function toggleTagFilter(tag, primaryKey, oppositeKey) {
    var u = new URL(window.location.href);
    var primary = parseTagParam(u.searchParams.get(primaryKey) || '');
    var opposite = parseTagParam(u.searchParams.get(oppositeKey) || '');

    var oi = opposite.indexOf(tag);
    if (oi >= 0) opposite.splice(oi, 1);

    var pi = primary.indexOf(tag);
    if (pi >= 0) { primary.splice(pi, 1); } else { primary.push(tag); }

    setTagParam(u, primaryKey, primary);
    setTagParam(u, oppositeKey, opposite);
    window.location.href = u.toString();
}

function includeTagFilter(tag) { toggleTagFilter(tag, 'tag', 'xtag'); }
function excludeTagFilter(tag) { toggleTagFilter(tag, 'xtag', 'tag'); }

function clearTagFilters() {
    var u = new URL(window.location.href);
    u.searchParams.delete('tag');
    u.searchParams.delete('xtag');
    window.location.href = u.toString();
}

function parseTagParam(s) {
    if (!s) return [];
    return s.split(',').map(function(t) { return t.trim(); }).filter(Boolean);
}

function setTagParam(u, key, tags) {
    if (tags.length > 0) {
        u.searchParams.set(key, tags.join(','));
    } else {
        u.searchParams.delete(key);
    }
}
