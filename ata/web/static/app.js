// Update the count badges next to column headers based on actual task rows.
function updateColumnCounts() {
    document.querySelectorAll('.task-list').forEach(function(list) {
        var count = list.querySelectorAll('.task-row').length;
        var heading = list.parentElement.querySelector('h2 .count');
        if (heading) heading.textContent = count;
    });
}

function sendReorder(id, position, opts) {
    var body = 'id=' + encodeURIComponent(id) + '&position=' + position;
    if (opts.parent) body += '&parent=' + encodeURIComponent(opts.parent);
    if (opts.oldParent !== undefined) body += '&oldParent=' + encodeURIComponent(opts.oldParent);
    if (opts.status) body += '&status=' + encodeURIComponent(opts.status);
    window._localActionUntil = Date.now() + 1000;
    fetch('/reorder', {
        method: 'POST',
        headers: {'Content-Type': 'application/x-www-form-urlencoded'},
        body: body
    }).then(function(resp) {
        if (!resp.ok) { console.error('reorder failed:', resp.status); window.location.reload(); }
    }).catch(function() { window.location.reload(); });
    updateColumnCounts();
}

document.addEventListener('DOMContentLoaded', function() {
    // Suppress SSE reloads briefly after local actions to avoid self-triggered reloads.
    window._localActionUntil = 0;

    function onSortEnd(evt) {
        var id = evt.item.dataset.id;
        var fromEpic = evt.from.dataset.epic || '';
        var toEpic = evt.to.dataset.epic || '';
        var status = evt.to.dataset.status || evt.to.closest('[data-status]').dataset.status;
        sendReorder(id, evt.newIndex, {
            status: status,
            parent: toEpic,
            oldParent: fromEpic !== toEpic ? fromEpic : undefined
        });
    }

    // Prevent dropping an epic-group into any of its own descendant containers.
    function isDropIntoOwnDescendant(evt) {
        if (!evt.dragged.classList.contains('epic-group') || !evt.to.classList.contains('epic-children')) {
            return false;
        }
        var draggedId = evt.dragged.dataset.id;
        var el = evt.to;
        while (el) {
            if (el.dataset && (el.dataset.id === draggedId || el.dataset.epic === draggedId)) {
                return true;
            }
            el = el.parentElement;
        }
        return false;
    }

    // Top-level sortable lists (queue, backlog columns).
    document.querySelectorAll('.task-list.sortable').forEach(function(el) {
        new Sortable(el, {
            group: 'workspace',
            handle: '.drag-handle',
            draggable: '.task-row, .epic-group',
            animation: 150,
            ghostClass: 'sortable-ghost',
            chosenClass: 'sortable-chosen',
            onEnd: onSortEnd,
            onMove: function(evt) {
                if (isDropIntoOwnDescendant(evt)) return false;
            }
        });
    });

    // Nested sortable lists (epic children), including deeply nested ones.
    document.querySelectorAll('.epic-children.sortable').forEach(function(el) {
        new Sortable(el, {
            group: 'workspace',
            handle: '.drag-handle',
            draggable: '.child-row, .task-row, .epic-group',
            animation: 150,
            ghostClass: 'sortable-ghost',
            chosenClass: 'sortable-chosen',
            onEnd: onSortEnd,
            onMove: function(evt) {
                if (isDropIntoOwnDescendant(evt)) return false;
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

    // Inline editing for body (task detail) and spec (epic detail).
    initClickToEdit('body-display', 'body-edit');
    initClickToEdit('spec-display', 'spec-edit');
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

// Copy markdown reference to clipboard.
function copyMarkdown(text) {
    navigator.clipboard.writeText(text).then(function() {
        // Brief visual feedback could be added here.
    });
}

// Generic click-to-edit initializer for body and spec sections.
function initClickToEdit(displayId, editId) {
    var display = document.getElementById(displayId);
    var edit = document.getElementById(editId);
    if (!display || !edit) return;
    display.addEventListener('click', function() {
        display.style.display = 'none';
        edit.style.display = 'block';
        var textarea = edit.querySelector('textarea');
        textarea.focus();
        textarea.selectionStart = textarea.value.length;
    });
    edit.addEventListener('htmx:afterRequest', function(e) {
        if (e.detail.successful) window.location.reload();
    });
}

// Called by Cancel buttons on edit forms.
function cancelEdit(prefix) {
    document.getElementById(prefix + '-display').style.display = '';
    document.getElementById(prefix + '-edit').style.display = 'none';
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
