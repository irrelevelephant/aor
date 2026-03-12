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
    var localActionUntil = 0;

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
                localActionUntil = Date.now() + 1000;
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
                if (Date.now() < localActionUntil) return;
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
