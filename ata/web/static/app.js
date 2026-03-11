document.addEventListener('DOMContentLoaded', function() {
    // Sortable.js on drag-to-reorder lists.
    // Queue and backlog share a group so tasks can be dragged between them.
    document.querySelectorAll('.sortable').forEach(function(el) {
        new Sortable(el, {
            group: 'tasks',
            animation: 150,
            ghostClass: 'sortable-ghost',
            chosenClass: 'sortable-chosen',
            onEnd: function(evt) {
                var id = evt.item.dataset.id;
                var targetStatus = evt.to.dataset.status;
                var body = 'id=' + encodeURIComponent(id) + '&position=' + evt.newIndex;
                if (targetStatus) {
                    body += '&status=' + encodeURIComponent(targetStatus);
                }
                fetch('/reorder', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/x-www-form-urlencoded'},
                    body: body
                });
            }
        });
    });

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
