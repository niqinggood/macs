(function () {
    'use strict';

    var headerSelector = '.modal-header, .team-modal-header, .pipeline-header, .todo-header, .artifact-header, .preview-header, .work-center-header';
    var surfaceSelector = '.modal-content, .team-modal, .pipeline-panel, .todo-card, .artifact-panel, .preview-content';
    var overlaySelector = '.modal, .team-overlay, .pipeline-overlay, .todo-panel, .artifact-overlay, .preview-modal';
    var closeSelector = '.close-modal, .team-modal-close, .pipeline-close, .todo-close, .artifact-close, .preview-close';
    var windows = new Map();
    var dock;
    var sizeProperties = ['width', 'max-width', 'height', 'max-height'];
    var icons = {
        close: '<path d="m5 5 6 6M11 5l-6 6"/>',
        minimize: '<path d="M4 8h8"/>',
        maximize: '<path d="M5 11V5h6M5 5l6 6"/>',
        restore: '<rect x="3" y="3" width="10" height="10" rx="2"/>'
    };

    function t(key, fallback) {
        return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
    }

    function icon(action) {
        return '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + icons[action] + '</svg>';
    }

    function title(state) {
        var heading = state.header.querySelector('h2, h3, h4');
        return (heading && heading.textContent.trim()) || t('windowRestore', '还原窗口');
    }

    function updateLabels(state) {
        var labels = {
            close: t('windowClose', '关闭窗口'),
            minimize: t('windowMinimize', '最小化窗口'),
            maximize: state.savedSize ? t('windowRestore', '还原窗口') : t('windowMaximize', '最大化窗口')
        };
        state.controls.querySelectorAll('button').forEach(function (button) {
            var action = button.dataset.windowAction;
            button.title = labels[action];
            button.setAttribute('aria-label', labels[action]);
            if (action === 'maximize') button.setAttribute('aria-pressed', state.savedSize ? 'true' : 'false');
        });
        state.header.querySelectorAll('[data-window-size-toggle]').forEach(function (button) {
            button.title = labels.maximize;
            button.setAttribute('aria-label', labels.maximize);
            button.setAttribute('aria-pressed', state.savedSize ? 'true' : 'false');
            var label = button.querySelector('[data-window-size-label]');
            if (label) label.textContent = state.savedSize ? t('windowRestore', '还原窗口') : t('windowMaximize', '最大化窗口');
        });
        if (state.restoreButton) {
            state.restoreButton.querySelector('span').textContent = title(state);
            var label = t('windowRestore', '还原窗口') + ': ' + title(state);
            state.restoreButton.title = label;
            state.restoreButton.setAttribute('aria-label', label);
        }
    }

    function restoreSize(state) {
        if (!state.savedSize) return;
        sizeProperties.forEach(function (property) {
            var previous = state.savedSize[property];
            if (previous.value) state.surface.style.setProperty(property, previous.value, previous.priority);
            else state.surface.style.removeProperty(property);
        });
        state.savedSize = null;
        state.surface.classList.remove('mac-window-maximized');
        updateLabels(state);
    }

    function toggleSize(state) {
        if (state.savedSize) {
            restoreSize(state);
            return;
        }
        state.savedSize = {};
        state.openClass = state.overlay.classList.contains('active') ? 'active' : (state.overlay.classList.contains('open') ? 'open' : '');
        sizeProperties.forEach(function (property) {
            state.savedSize[property] = {
                value: state.surface.style.getPropertyValue(property),
                priority: state.surface.style.getPropertyPriority(property)
            };
        });
        state.surface.style.setProperty('width', 'calc(100vw - var(--mac-window-inset, 48px))', 'important');
        state.surface.style.setProperty('max-width', 'none', 'important');
        state.surface.style.setProperty('height', 'calc(100dvh - var(--mac-window-inset, 48px))', 'important');
        state.surface.style.setProperty('max-height', 'calc(100dvh - var(--mac-window-inset, 48px))', 'important');
        state.surface.classList.add('mac-window-maximized');
        updateLabels(state);
    }

    function clearMinimized(state) {
        state.overlay.classList.remove('mac-window-minimized');
        if (state.restoreButton) state.restoreButton.remove();
        state.restoreButton = null;
        dock.hidden = !dock.childElementCount;
    }

    function minimize(state) {
        if (state.restoreButton) return;
        state.openClass = state.overlay.classList.contains('active') ? 'active' : (state.overlay.classList.contains('open') ? 'open' : '');
        var restore = document.createElement('button');
        restore.type = 'button';
        restore.className = 'mac-window-restore';
        restore.innerHTML = icon('restore') + '<span></span>';
        restore.addEventListener('click', function () {
            clearMinimized(state);
            state.controls.querySelector('[data-window-action="minimize"]').focus();
        });
        state.restoreButton = restore;
        dock.appendChild(restore);
        dock.hidden = false;
        updateLabels(state);
        state.overlay.classList.add('mac-window-minimized');
        restore.focus();
    }

    function enhance(header) {
        var surface = header.closest(surfaceSelector);
        var overlay = surface && surface.closest(overlaySelector);
        var nativeClose = header.querySelector(closeSelector);
        // Inline artifact headers and surfaces without a close contract stay untouched.
        if (!overlay || !nativeClose) return;
        var previous = windows.get(overlay);
        if (previous && previous.header.isConnected) return;
        if (previous && previous.restoreButton) clearMinimized(previous);
        var controls = document.createElement('div');
        controls.className = 'mac-window-controls';
        var state = {header: header, surface: surface, overlay: overlay, controls: controls, savedSize: null, restoreButton: null, openClass: ''};
        ['close', 'minimize', 'maximize'].forEach(function (action) {
            var button = document.createElement('button');
            button.type = 'button';
            button.className = 'mac-window-control';
            button.dataset.windowAction = action;
            button.innerHTML = icon(action);
            button.addEventListener('click', function (event) {
                event.preventDefault();
                event.stopPropagation();
                // Preserve each window's cleanup and unsaved-change confirmation.
                if (action === 'close') nativeClose.click();
                else if (action === 'minimize') minimize(state);
                else toggleSize(state);
            });
            controls.appendChild(button);
        });
        windows.set(overlay, state);
        header.classList.add('mac-window-header');
        header.appendChild(controls);
        updateLabels(state);
    }

    function scan(root) {
        if (root.matches && root.matches(headerSelector)) enhance(root);
        if (root.querySelectorAll) root.querySelectorAll(headerSelector).forEach(enhance);
    }

    window.restoreMacWindow = function (overlay) {
        var state = windows.get(overlay);
        if (state && state.restoreButton) clearMinimized(state);
    };

    window.toggleMacWindowSize = function (overlay) {
        if (!overlay || !overlay.querySelector) return;
        if (!windows.has(overlay)) scan(overlay);
        var state = windows.get(overlay);
        if (state) toggleSize(state);
    };

    function init() {
        dock = document.createElement('div');
        dock.className = 'mac-window-dock';
        dock.hidden = true;
        dock.setAttribute('role', 'region');
        dock.setAttribute('aria-label', t('windowMinimized', '已最小化的窗口'));
        document.body.appendChild(dock);
        scan(document);
        new MutationObserver(function (records) {
            records.forEach(function (record) {
                if (record.type === 'childList') record.addedNodes.forEach(scan);
                var state = windows.get(record.target);
                if (!state) return;
                var closed = state.overlay.hidden || state.overlay.style.display === 'none' ||
                    (state.openClass && !state.overlay.classList.contains(state.openClass));
                if (closed) {
                    if (state.restoreButton) clearMinimized(state);
                    restoreSize(state);
                }
            });
            windows.forEach(function (state, overlay) {
                if (!overlay.isConnected) {
                    if (state.restoreButton) clearMinimized(state);
                    windows.delete(overlay);
                }
            });
        }).observe(document.body, {childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style', 'hidden']});
        document.addEventListener('openkitty:languagechange', function () {
            dock.setAttribute('aria-label', t('windowMinimized', '已最小化的窗口'));
            windows.forEach(updateLabels);
        });
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, {once: true});
    else init();
})();
