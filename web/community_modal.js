(function () {
    'use strict';
    var modal, frame, returnFocus;

    function close() {
        if (!modal) return;
        modal.style.display = 'none';
        frame.src = 'about:blank';
        modal.removeAttribute('data-post-id');
        if (returnFocus && returnFocus.isConnected) returnFocus.focus();
    }

    function create() {
        modal = document.createElement('div');
        modal.id = 'communityReaderModal';
        modal.className = 'modal community-reader-modal';
        modal.style.display = 'none';
        modal.setAttribute('role', 'dialog');
        modal.setAttribute('aria-modal', 'true');
        modal.setAttribute('aria-labelledby', 'communityReaderHeading');
        modal.innerHTML = '<section class="modal-content community-reader-surface">' +
            '<header class="modal-header"><h2 id="communityReaderHeading">科技前沿</h2>' +
            '<button type="button" class="close-modal" title="关闭阅读窗口" aria-label="关闭阅读窗口">×</button></header>' +
            '<iframe title="科技前沿文章" class="community-reader-frame" referrerpolicy="no-referrer"></iframe></section>';
        frame = modal.querySelector('iframe');
        modal.querySelector('.close-modal').addEventListener('click', close);
        modal.addEventListener('click', function (event) { if (event.target === modal) close(); });
        modal.addEventListener('keydown', function (event) {
            if (event.key === 'Escape') { event.preventDefault(); close(); }
            if (event.key === 'Tab' && event.shiftKey && event.target === modal.querySelector('.mac-window-control')) {
                event.preventDefault(); frame.focus();
            }
        });
        document.body.appendChild(modal);
    }

    window.openCommunityReader = function (id, trigger) {
        if (!Number.isSafeInteger(Number(id)) || Number(id) <= 0) return;
        if (!modal) create();
        returnFocus = trigger || document.activeElement;
        if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(modal);
        modal.style.display = 'flex';
        if (modal.dataset.postId !== String(id)) {
            modal.dataset.postId = String(id);
            frame.src = '/web/community.html?id=' + encodeURIComponent(id) + '&embedded=1';
        }
        modal.querySelector('.close-modal').focus();
    };
    window.addEventListener('message', function (event) {
        if (!modal || event.origin !== location.origin || event.source !== frame.contentWindow || !event.data) return;
        if (event.data.type === 'macs:reader-close') close();
        if (event.data.type === 'macs:reader-focus') {
            var buttons = modal.querySelectorAll('header button');
            var button = event.data.edge === 'first' ? buttons[0] : buttons[buttons.length - 1];
            if (button) button.focus();
        }
    });
})();
