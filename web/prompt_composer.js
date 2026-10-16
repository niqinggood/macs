(function () {
    'use strict';

    // Presentation only: the existing textarea remains the sole draft and
    // sending source. Never substitute a template name for its actual value.
    var COPY = {
        zh: {
            promptComposerExpand: '显示完整提示词',
            promptComposerCollapse: '收起完整提示词',
            promptComposerEdited: '已编辑',
            promptComposerLabel: '已应用的提示词模板'
        },
        en: {
            promptComposerExpand: 'Show full prompt',
            promptComposerCollapse: 'Collapse full prompt',
            promptComposerEdited: 'Edited',
            promptComposerLabel: 'Applied prompt template'
        }
    };
    var input, container, summary, prefixNode, toggle, nameNode, editedNode;
    var state = null, composing = false, ready = false;

    function t(key) {
        var lang = String(window.openKittyLanguage || document.documentElement.lang || 'zh').toLowerCase().indexOf('en') === 0 ? 'en' : 'zh';
        var fallback = COPY[lang][key] || key;
        return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
    }
    function sessionID() { return typeof currentSessionId === 'undefined' ? null : currentSessionId; }
    function node(tag, cls, id) {
        var item = document.createElement(tag);
        if (cls) item.className = cls;
        if (id) item.id = id;
        return item;
    }
    function describe(active) {
        var ids = (input.getAttribute('aria-describedby') || '').split(/\s+/).filter(function (id) { return id && id !== 'promptComposerSummary'; });
        if (active) ids.push('promptComposerSummary');
        if (ids.length) input.setAttribute('aria-describedby', ids.join(' '));
        else input.removeAttribute('aria-describedby');
    }
    function render() {
        if (!ready) return;
        summary.hidden = !state;
        container.classList.toggle('prompt-composer-folded', !!state && state.folded);
        summary.classList.toggle('is-expanded', !!state && !state.folded);
        if (!state) return;
        var title = state.titleKey && typeof window.openKittyT === 'function' ? window.openKittyT(state.titleKey, state.title) : state.title;
        nameNode.textContent = title;
        prefixNode.textContent = state.prefix;
        prefixNode.hidden = !state.folded || !state.prefix.length;
        editedNode.textContent = t('promptComposerEdited');
        editedNode.hidden = !state.edited;
        summary.setAttribute('aria-label', t('promptComposerLabel'));
        toggle.title = t(state.folded ? 'promptComposerExpand' : 'promptComposerCollapse') + ' · ' + title;
        toggle.setAttribute('aria-label', toggle.title);
        toggle.setAttribute('aria-expanded', String(!state.folded));
    }
    function clear() {
        if (!ready || !state) return;
        state = null;
        describe(false);
        render();
    }
    function unfold(focus) {
        if (!state || !state.folded) return;
        state.folded = false;
        render();
        if (focus) input.focus({preventScroll: true});
    }
    function editable() {
        return !input.disabled && !input.readOnly && (typeof window.isSessionEditable !== 'function' || window.isSessionEditable());
    }
    function syncSession() {
        if (state && (state.session !== sessionID() || !editable())) clear();
    }
    function watchValue() {
        // Observe only this textarea instance. Native typing still uses its
        // normal input event; programmatic clears/replacements are immediate,
        // including successful sends and failed generation draft restoration.
        if (Object.prototype.hasOwnProperty.call(input, 'value')) return false;
        var descriptor = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value');
        if (!descriptor || !descriptor.get || !descriptor.set) return false;
        try {
            Object.defineProperty(input, 'value', {
                configurable: true,
                enumerable: descriptor.enumerable,
                get: function () { return descriptor.get.call(this); },
                set: function (value) {
                    var previous = descriptor.get.call(this);
                    descriptor.set.call(this, value);
                    if (state && descriptor.get.call(this) !== previous) clear();
                }
            });
            return true;
        } catch (_) { return false; }
    }
    function init() {
        if (ready) return true;
        input = document.getElementById('messageInput');
        container = input && input.closest('.input-container');
        if (!input || !container || !watchValue()) return false;
        summary = node('div', 'prompt-composer-summary', 'promptComposerSummary');
        summary.hidden = true;
        summary.setAttribute('role', 'group');
        prefixNode = node('div', 'prompt-composer-prefix', 'promptComposerPrefix');
        var row = node('div', 'prompt-composer-row');
        toggle = node('button', 'prompt-composer-toggle', 'promptComposerToggle');
        toggle.type = 'button';
        toggle.setAttribute('aria-controls', 'messageInput');
        var mark = node('span', 'prompt-composer-mark');
        mark.textContent = '✦';
        mark.setAttribute('aria-hidden', 'true');
        nameNode = node('span', 'prompt-composer-name');
        var arrow = node('span', 'prompt-composer-chevron');
        arrow.setAttribute('aria-hidden', 'true');
        toggle.append(mark, nameNode, arrow);
        editedNode = node('span', 'prompt-composer-edited', 'promptComposerEdited');
        editedNode.hidden = true;
        row.append(toggle, editedNode);
        summary.append(prefixNode, row);
        // Keep the original textarea in place so lazy media controls, uploads,
        // resizing and every existing listener keep their current references.
        input.after(summary);
        ready = true;
        toggle.addEventListener('click', function () {
            syncSession();
            if (!state) return;
            if (state.folded) unfold(true);
            else { state.folded = true; render(); input.focus({preventScroll: true}); }
        });
        summary.addEventListener('click', function (event) {
            if (state && state.folded && !event.target.closest('button')) unfold(true);
        });
        input.addEventListener('beforeinput', function () { unfold(false); });
        input.addEventListener('paste', function () { unfold(false); }, true);
        input.addEventListener('cut', function () { unfold(false); }, true);
        input.addEventListener('compositionstart', function () { composing = true; unfold(false); }, true);
        input.addEventListener('compositionend', function () { composing = false; }, true);
        input.addEventListener('keydown', function (event) {
            // Do not let the legacy Enter listener send while an IME is
            // confirming a word. Keep the default IME operation intact.
            if (event.key === 'Enter' && (composing || event.isComposing || event.keyCode === 229)) { event.stopImmediatePropagation(); return; }
            if (state && (event.key.indexOf('Arrow') === 0 || ['Home', 'End', 'Backspace', 'Delete'].indexOf(event.key) >= 0 || (event.key === 'Enter' && event.shiftKey) || ((event.ctrlKey || event.metaKey) && ['a', 'c', 'x', 'v'].indexOf(event.key.toLowerCase()) >= 0))) unfold(false);
        }, true);
        input.addEventListener('input', function () {
            if (!state) return;
            if (!input.value.length) { clear(); return; }
            if (input.value !== state.value) {
                state.value = input.value;
                state.edited = true;
                // A changed prefix cannot be represented by an old draft.
                // Subsequent collapse is explicit and covers the edited text.
                if (state.prefix && input.value.indexOf(state.prefix + '\n\n') !== 0) state.prefix = '';
                state.folded = false;
                render();
            }
        });
        new MutationObserver(function () { if (state && !editable()) clear(); }).observe(input, {attributes: true, attributeFilter: ['disabled', 'readonly']});
        var resize = document.getElementById('messageInputResizeHandle');
        if (resize) ['pointerdown', 'keydown', 'dblclick'].forEach(function (eventName) { resize.addEventListener(eventName, function () { unfold(false); }, true); });
        return true;
    }
    window.setPromptComposerTemplate = function (options) {
        if (!init() || !options || !editable() || input.value !== options.value || !input.value.length) return false;
        state = {
            title: String(options.title || ''),
            titleKey: typeof options.titleKey === 'string' ? options.titleKey : '',
            prefix: typeof options.prefix === 'string' && options.prefix.length && input.value.indexOf(options.prefix + '\n\n') === 0 ? options.prefix : '',
            value: input.value,
            session: sessionID(),
            folded: true,
            edited: false
        };
        describe(true);
        render();
        return true;
    };
    window.clearPromptComposerTemplate = clear;
    window.capturePromptComposerDraft = function () { return state ? Object.assign({}, state) : null; };
    window.restorePromptComposerDraft = function (snapshot) {
        if (!snapshot || !init() || !editable() || snapshot.session !== sessionID() || input.value !== snapshot.value) return false;
        state = Object.assign({}, snapshot);
        describe(true);
        render();
        return true;
    };
    window.syncPromptComposerSession = syncSession;
    document.addEventListener('openkitty:languagechange', render);
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, {once: true});
    else init();
})();
